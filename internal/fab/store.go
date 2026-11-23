package fab

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // driver en Go puro: sin compilador de C
)

// factory.db guarda el estado de las tareas, sus eventos y lo ya visto de cada fuente. Los
// artefactos de los expertos (spec, build, hallazgos, capturas) siguen como archivos en runs/<id>/.
// Antes era un estado.json por tarea: dos procesos (la fábrica y un comando de la terminal) se
// pisaban, y un archivo cortado frenaba todo.

const esquema = `
CREATE TABLE IF NOT EXISTS tasks (
	id TEXT PRIMARY KEY, titulo TEXT NOT NULL DEFAULT '', estado TEXT NOT NULL, paso TEXT NOT NULL DEFAULT '',
	vuelta INTEGER NOT NULL DEFAULT 0, motivo TEXT NOT NULL DEFAULT '', costo_usd REAL NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS events (
	tarea TEXT NOT NULL, ts INTEGER NOT NULL, paso TEXT NOT NULL, estado TEXT NOT NULL,
	mensaje TEXT NOT NULL, costo_usd REAL NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS events_tarea ON events(tarea);
CREATE INDEX IF NOT EXISTS events_ts ON events(ts);
CREATE TABLE IF NOT EXISTS vistos (clave TEXT PRIMARY KEY, ts INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS decisiones (tarea TEXT NOT NULL, ts INTEGER NOT NULL, texto TEXT NOT NULL, usada INTEGER NOT NULL DEFAULT 0);
`

var (
	dbsMu sync.Mutex
	dbs   = map[string]*sql.DB{}
)

// abrirDB abre (una vez por proceso) la base de un runs/. WAL + busy_timeout: la fábrica, la
// página y los comandos de la terminal leen y escriben a la vez sin trabarse.
func abrirDB(runs string) (*sql.DB, error) {
	dbsMu.Lock()
	defer dbsMu.Unlock()
	if db, ok := dbs[runs]; ok {
		return db, nil
	}
	if err := os.MkdirAll(runs, 0o755); err != nil {
		return nil, err
	}
	ruta := filepath.Join(runs, "factory.db")
	nueva := false
	if _, err := os.Stat(ruta); err != nil {
		nueva = true
	}
	db, err := sql.Open("sqlite", "file:"+ruta+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(wal)&_pragma=synchronous(normal)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(esquema); err != nil {
		db.Close()
		return nil, fmt.Errorf("esquema de factory.db: %w", err)
	}
	// Columnas que se sumaron después: en una base vieja se agregan; si ya están, SQLite avisa y sigue.
	if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN prioridad INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		db.Close()
		return nil, fmt.Errorf("migrar factory.db: %w", err)
	}
	if nueva {
		if err := importarArchivos(db, runs); err != nil {
			db.Close()
			_ = os.Remove(ruta)
			return nil, fmt.Errorf("importar runs/ a factory.db: %w", err)
		}
	}
	dbs[runs] = db
	return db, nil
}

func mustDB(runs string) *sql.DB {
	db, err := abrirDB(runs)
	if err != nil {
		panic(err) // sin base no hay fábrica: falla fuerte y con el motivo
	}
	return db
}

// importarArchivos trae lo de antes de la base: estado.json y events.jsonl de cada tarea, lo ya
// visto y el gasto del Jefe y del Bibliotecario a pedido. Corre una sola vez, al crear la base.
func importarArchivos(db *sql.DB, runs string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	estados, _ := filepath.Glob(filepath.Join(runs, "*", "estado.json"))
	sort.Strings(estados)
	for _, p := range estados {
		var e Estado
		b, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(b, &e) != nil || e.ID == "" {
			fmt.Fprintf(os.Stderr, "importar: se omite %s (no se pudo leer)\n", p)
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tasks(id,titulo,estado,paso,vuelta,motivo,costo_usd) VALUES(?,?,?,?,?,?,?)`,
			e.ID, e.Titulo, e.Estado, e.Paso, e.Vuelta, e.Motivo, e.CostUSD); err != nil {
			return err
		}
		if err := importarEventos(tx, filepath.Join(filepath.Dir(p), "events.jsonl"), e.ID); err != nil {
			return err
		}
	}
	for _, lado := range []string{"jefe", "lado"} {
		if err := importarEventos(tx, filepath.Join(runs, ".cache", lado+".jsonl"), lado); err != nil {
			return err
		}
	}
	if b, err := os.ReadFile(filepath.Join(runs, ".cache", "vistos.json")); err == nil {
		v := map[string]bool{}
		_ = json.Unmarshal(b, &v)
		for k := range v {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO vistos(clave,ts) VALUES(?,?)`, k, time.Now().UnixNano()); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func importarEventos(tx *sql.Tx, ruta, tarea string) error {
	b, err := os.ReadFile(ruta)
	if err != nil {
		return nil // sin eventos
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	for dec.More() {
		var ev Event
		if dec.Decode(&ev) != nil {
			break // una línea cortada al final: lo de antes se conserva
		}
		if _, err := tx.Exec(`INSERT INTO events(tarea,ts,paso,estado,mensaje,costo_usd) VALUES(?,?,?,?,?,?)`,
			tarea, ev.TS.UnixNano(), ev.Paso, ev.Estado, ev.Mensaje, ev.CostUSD); err != nil {
			return err
		}
	}
	return nil
}

// LogLado registra un gasto que no es de una tarea (el Jefe, el Bibliotecario a pedido): suma al
// tope diario igual.
func LogLado(runs, lado, paso, estado, msg string, cost float64) {
	db, err := abrirDB(runs)
	if err == nil {
		_, err = db.Exec(`INSERT INTO events(tarea,ts,paso,estado,mensaje,costo_usd) VALUES(?,?,?,?,?,?)`,
			lado, time.Now().UnixNano(), paso, estado, msg, cost)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "no se pudo registrar el gasto de %s: %v\n", lado, err)
	}
}

// RecuperarEnCurso: al arrancar la fábrica, lo que quedó EN_CURSO (se cortó la luz, se durmió la
// Mac, se cerró la pestaña) vuelve a la cola. Devuelve cuántas.
func RecuperarEnCurso(runs string) (int, error) {
	db, err := abrirDB(runs)
	if err != nil {
		return 0, err
	}
	r, err := db.Exec(`UPDATE tasks SET estado=?, motivo='se retomó: la fábrica se había cortado a mitad' WHERE estado=?`, Pendiente, EnCurso)
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	return int(n), nil
}

// Las respuestas del operador (fabrica continuar) viven en la base, no en un JSON al lado del worktree:
// un Builder con Bash podía escribir ../../continuar.json y su texto llegaba como "canal confiable".
// ponytail: sin sandbox el Builder igual puede tocar la base; esto saca el archivo obvio del camino.

// GuardarDecision anota una respuesta del operador para la tarea, pendiente de usar.
func GuardarDecision(runs, tarea, texto string) error {
	db, err := abrirDB(runs)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO decisiones(tarea, ts, texto) VALUES(?,?,?)`, tarea, time.Now().UnixNano(), texto)
	return err
}

// DecisionesDe devuelve todas las respuestas del operador de la tarea, en orden, y si hay alguna
// sin usar (lo que retoma una spec que frenó).
func DecisionesDe(runs, tarea string) (todas string, nuevas bool, err error) {
	db, err := abrirDB(runs)
	if err != nil {
		return "", false, err
	}
	rows, err := db.Query(`SELECT texto, usada FROM decisiones WHERE tarea=? ORDER BY ts`, tarea)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	var partes []string
	for rows.Next() {
		var texto string
		var usada int
		if err := rows.Scan(&texto, &usada); err != nil {
			return "", false, err
		}
		partes = append(partes, texto)
		nuevas = nuevas || usada == 0
	}
	return strings.Join(partes, "\n\n"), nuevas, rows.Err()
}

// MarcarDecisionesUsadas: las respuestas ya retomaron la spec.
func MarcarDecisionesUsadas(runs, tarea string) error {
	db, err := abrirDB(runs)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE decisiones SET usada=1 WHERE tarea=?`, tarea)
	return err
}
