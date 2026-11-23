package fab

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Estados de una tarea, en el orden del diagrama.
const (
	Pendiente           = "PENDIENTE"
	EnCurso             = "EN_CURSO"
	EsperandoAprobacion = "ESPERANDO_APROBACION"
	Trabada             = "TRABADA"
	Descartada          = "DESCARTADA" // una propuesta que el operador no quiso
	PRAbierto           = "PR_ABIERTO" // después de factory aprobación
)

// Entrada es 00-entrada.json.
type Entrada struct {
	ID     string `json:"id"`
	Texto  string `json:"texto"`
	Fuente string `json:"fuente"` // prompt | ticket | tarjeta | log
	// Cliente: el slug del ERP de donde vino. Vacío si no se sabe (lo deduce el Spec writer).
	Cliente string `json:"cliente,omitempty"`
	// Origen: la referencia del ticket en su sistema (owner/repo#n si vino de github-issues).
	Origen string `json:"origen,omitempty"`
	// Sensible lo marca una regla (plata, auth, datos), no el LLM.
	Sensible []string  `json:"sensible"`
	Creada   time.Time `json:"creada"`
}

// Estado es estado.json: lo que lee `fabrica status` (y después la consola).
type Estado struct {
	ID      string  `json:"id"`
	Titulo  string  `json:"titulo"`
	Estado  string  `json:"estado"`
	Paso    string  `json:"paso"`
	Vuelta  int     `json:"vuelta"`
	Motivo  string  `json:"motivo,omitempty"`
	CostUSD float64 `json:"costo_usd"`
}

// Event es una línea de events.jsonl.
type Event struct {
	TS      time.Time `json:"ts"`
	ID      string    `json:"id"`
	Paso    string    `json:"paso"`
	Estado  string    `json:"estado"` // inicio | ok | falla
	Mensaje string    `json:"mensaje"`
	CostUSD float64   `json:"costo_usd"`
}

// Task es una carpeta runs/<id>/ (los artefactos) y una fila de factory.db (el estado).
type Task struct {
	Dir  string
	Est  Estado
	mu   sync.Mutex // los revisores loguean en paralelo
	runs string
	// sinCuota: algún experto no se lanzó por el tope del día; la tarea vuelve a la cola.
	sinCuota bool
	// repiteInfra: la vuelta en curso repite una que cayó por el entorno; el Builder retoma su sesión.
	repiteInfra bool
}

func newID() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102-1504") + "-" + hex.EncodeToString(b)
}

// AddTask crea la tarea ya en su estado (PENDIENTE o PROPUESTA), en una sola operación: un log
// nunca pasa por PENDIENTE. Si el ID se repite (mismo minuto), prueba otro: nunca pisa una tarea.
func AddTask(runs, texto, fuente, estado, cliente string) (*Task, error) {
	return addTaskDesde(runs, texto, fuente, estado, cliente, "")
}

// addTaskDesde es AddTask con la referencia del ticket de origen.
func addTaskDesde(runs, texto, fuente, estado, cliente, origen string) (*Task, error) {
	db, err := abrirDB(runs)
	if err != nil {
		return nil, err
	}
	for intento := 0; intento < 20; intento++ {
		e := Entrada{ID: newID(), Texto: texto, Fuente: fuente, Cliente: cliente, Origen: origen, Sensible: Sensible(texto), Creada: time.Now()}
		dir := filepath.Join(runs, e.ID)
		if err := os.Mkdir(dir, 0o755); err != nil {
			if os.IsExist(err) {
				continue
			}
			return nil, fmt.Errorf("crear la tarea: %w", err)
		}
		t := &Task{Dir: dir, runs: runs, Est: Estado{ID: e.ID, Titulo: texto, Estado: estado, Paso: "01-entrada"}}
		if err := t.WriteJSON("00-entrada.json", e); err != nil {
			return nil, err
		}
		if _, err := db.Exec(`INSERT INTO tasks(id,titulo,estado,paso) VALUES(?,?,?,?)`, e.ID, t.Est.Titulo, estado, t.Est.Paso); err != nil {
			return nil, fmt.Errorf("guardar la tarea: %w", err)
		}
		return t, nil
	}
	return nil, fmt.Errorf("crear la tarea: no hay un ID libre")
}

// LoadTasks devuelve todas las tareas, de la más vieja a la más nueva.
func LoadTasks(runs string) ([]*Task, error) {
	db, err := abrirDB(runs)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id,titulo,estado,paso,vuelta,motivo,costo_usd FROM tasks ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ts []*Task
	for rows.Next() {
		t := &Task{runs: runs}
		e := &t.Est
		if err := rows.Scan(&e.ID, &e.Titulo, &e.Estado, &e.Paso, &e.Vuelta, &e.Motivo, &e.CostUSD); err != nil {
			return nil, err
		}
		t.Dir = filepath.Join(runs, e.ID)
		ts = append(ts, t)
	}
	return ts, rows.Err()
}

func (t *Task) WriteJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("serializar %s: %w", name, err)
	}
	return os.WriteFile(filepath.Join(t.Dir, name), b, 0o644)
}

func (t *Task) ReadJSON(name string, v any) error {
	b, err := os.ReadFile(filepath.Join(t.Dir, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Log agrega un evento y deja al día paso, costo, título y vuelta. Nunca toca el estado: eso solo
// lo cambia Transicion, con la condición de desde dónde. Así un evento tardío (el Bibliotecario
// corriendo después de la aprobación) no pisa una PR abierta.
func (t *Task) Log(paso, estado, msg string, cost float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Est.Paso = paso
	t.Est.CostUSD += cost
	db, err := abrirDB(t.runs)
	if err == nil {
		_, err = db.Exec(`INSERT INTO events(tarea,ts,paso,estado,mensaje,costo_usd) VALUES(?,?,?,?,?,?)`,
			t.Est.ID, time.Now().UnixNano(), paso, estado, msg, cost)
	}
	if err == nil {
		_, err = db.Exec(`UPDATE tasks SET paso=?, costo_usd=costo_usd+?, titulo=?, vuelta=? WHERE id=?`,
			paso, cost, t.Est.Titulo, t.Est.Vuelta, t.Est.ID)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "no se pudo guardar el evento de %s: %v\n", t.Est.ID, err)
	}
}

// Transicion cambia el estado solo si la tarea sigue en desde. Si otro proceso ya la movió, no
// pisa nada y devuelve error.
func (t *Task) Transicion(desde, hacia, motivo string) error {
	if err := SetEstado(t.runs, t.Est.ID, desde, hacia, motivo); err != nil {
		return err
	}
	t.mu.Lock()
	t.Est.Estado, t.Est.Motivo = hacia, motivo
	t.mu.Unlock()
	t.Log(t.Est.Paso, hacia, motivo, 0)
	return nil
}

// Finish deja la tarea en su estado final, desde el estado en que la tiene este proceso.
func (t *Task) Finish(estado, motivo string) {
	if err := t.Transicion(t.Est.Estado, estado, motivo); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", t.Est.ID, err)
	}
}

// SetEstado mueve una tarea de desde a hacia, solo si sigue en desde (una sola operación).
func SetEstado(runs, id, desde, hacia, motivo string) error {
	db, err := abrirDB(runs)
	if err != nil {
		return err
	}
	r, err := db.Exec(`UPDATE tasks SET estado=?, motivo=? WHERE id=? AND estado=?`, hacia, motivo, id, desde)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		var actual string
		if db.QueryRow(`SELECT estado FROM tasks WHERE id=?`, id).Scan(&actual) != nil {
			return fmt.Errorf("no existe la tarea %s", id)
		}
		return fmt.Errorf("%s está en %s, no en %s", id, actual, desde)
	}
	return nil
}

// SpentToday suma lo gastado hoy en las tareas y en lo que corre fuera de ellas (el Jefe, el
// Bibliotecario a pedido), para el tope diario.
func SpentToday(runs string) float64 {
	db, err := abrirDB(runs)
	if err != nil {
		return 0
	}
	y, m, d := time.Now().Date()
	desde := time.Date(y, m, d, 0, 0, 0, 0, time.Local).UnixNano()
	var total sql.NullFloat64
	_ = db.QueryRow(`SELECT SUM(costo_usd) FROM events WHERE ts >= ?`, desde).Scan(&total)
	return total.Float64
}

// LoadEvents devuelve los eventos de la tarea, en orden.
func (t *Task) LoadEvents() []Event {
	db, err := abrirDB(t.runs)
	if err != nil {
		return nil
	}
	rows, err := db.Query(`SELECT ts,paso,estado,mensaje,costo_usd FROM events WHERE tarea=? ORDER BY ts, rowid`, t.Est.ID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var evs []Event
	for rows.Next() {
		var ns int64
		ev := Event{ID: t.Est.ID}
		if rows.Scan(&ns, &ev.Paso, &ev.Estado, &ev.Mensaje, &ev.CostUSD) == nil {
			ev.TS = time.Unix(0, ns)
			evs = append(evs, ev)
		}
	}
	return evs
}
