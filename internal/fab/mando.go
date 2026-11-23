package fab

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Los comandos de la terminal, el único mando de la fábrica. Cada uno cambia el estado con la misma
// regla que la línea: solo desde el estado esperado (SetEstado), así no pisa lo que la fábrica movió.

// SetPrioridad: más alta, antes. Empate: la más vieja primero.
func SetPrioridad(runs, id string, p int) error {
	r, err := mustDB(runs).Exec(`UPDATE tasks SET prioridad=? WHERE id=?`, p, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return fmt.Errorf("no existe la tarea %s", id)
	}
	return nil
}

// SiguientePendiente: la próxima tarea que toma la línea.
// Carril: una tarea por cliente a la vez. Las de clientes distintos corren en paralelo; sin cliente
// conocido (un prompt sin --proyecto) van todas por un carril propio, de a una.
func Carril(runs, id string) string {
	var e Entrada
	t := &Task{Dir: filepath.Join(runs, id), runs: runs}
	if t.ReadJSON("00-entrada.json", &e) != nil || e.Cliente == "" {
		return "sin-cliente"
	}
	return e.Cliente
}

// SiguienteLibre: la pendiente de más prioridad cuyo cliente no tiene otra tarea corriendo.
func SiguienteLibre(runs string, ocupado map[string]bool) (*Task, string, error) {
	rows, err := mustDB(runs).Query(`SELECT id FROM tasks WHERE estado=? ORDER BY prioridad DESC, rowid`, Pendiente)
	if err != nil {
		return nil, "", err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, "", err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if c := Carril(runs, id); !ocupado[c] {
			t, err := findTask(runs, id)
			return t, c, err
		}
	}
	return nil, "", nil
}

func SiguientePendiente(runs string) (*Task, error) {
	var id string
	err := mustDB(runs).QueryRow(`SELECT id FROM tasks WHERE estado=? ORDER BY prioridad DESC, rowid LIMIT 1`, Pendiente).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, nil
		}
		return nil, err
	}
	return findTask(runs, id)
}

// Aceptar pasa una propuesta a la cola. Las tarjetas del cerebro (contradicciones, rebase que choca)
// se arreglan a mano en el repo de conocimiento: no son trabajo para la línea, solo se descartan.
func Aceptar(runs, id string) error {
	var e Entrada
	t := &Task{Dir: filepath.Join(runs, id), runs: runs}
	if t.ReadJSON("00-entrada.json", &e) == nil && e.Fuente == "cerebro" {
		return fmt.Errorf("%s es un aviso del cerebro: se arregla en el repo de conocimiento y se descarta con `fabrica descartar %s`", id, id)
	}
	return SetEstado(runs, id, Propuesta, Pendiente, "aceptada por el operador")
}

// Reintentar: una tarea trabada vuelve a la cola (después de que el operador arregló lo que faltaba).
func Reintentar(runs, id string) error {
	return SetEstado(runs, id, Trabada, Pendiente, "reintento pedido por el operador")
}

// Cancelar descarta una tarea que no está corriendo. Una EN_CURSO se para con Ctrl-C en la fábrica
// (vuelve a PENDIENTE) y recién ahí se cancela.
func Cancelar(runs, id string) error {
	t, err := findTask(runs, id)
	if err != nil {
		return err
	}
	switch t.Est.Estado {
	case Pendiente, Propuesta, Trabada, EsperandoAprobacion:
		if err := SetEstado(runs, id, t.Est.Estado, Descartada, "cancelada por el operador"); err != nil {
			return err
		}
		StopPreviewDe(runs, id)
		return nil
	case EnCurso:
		return fmt.Errorf("%s está corriendo: hay que detener la fábrica (Ctrl-C la devuelve a la cola) y cancelarla después", id)
	}
	return fmt.Errorf("%s está en %s: no hay nada que cancelar", id, t.Est.Estado)
}

// La pausa es un archivo: la ve la fábrica en cada vuelta del loop, sin señales ni procesos.
func pausaPath(runs string) string { return filepath.Join(runs, ".cache", "pausa") }

func Pausar(runs string) error {
	if err := os.MkdirAll(filepath.Dir(pausaPath(runs)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pausaPath(runs), []byte(time.Now().Format(time.RFC3339)), 0o644)
}

func Seguir(runs string) error {
	if err := os.Remove(pausaPath(runs)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Pausada: la fábrica no toma tareas nuevas (la que corre termina).
func Pausada(runs string) bool {
	_, err := os.Stat(pausaPath(runs))
	return err == nil
}

// Ver es el detalle de una tarea para la terminal: estado, de dónde vino, los eventos y los archivos.
func Ver(runs, id string) (string, error) {
	t, err := findTask(runs, id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	e := t.Est
	fmt.Fprintf(&b, "%s  %s  paso %s  vuelta %d  US$%.2f\n%s\n", e.ID, e.Estado, e.Paso, e.Vuelta, e.CostUSD, e.Titulo)
	if e.Motivo != "" {
		fmt.Fprintf(&b, "motivo: %s\n", e.Motivo)
	}
	var en Entrada
	if t.ReadJSON("00-entrada.json", &en) == nil {
		fmt.Fprintf(&b, "entró por %s", en.Fuente)
		if en.Cliente != "" {
			fmt.Fprintf(&b, " · proyecto %s", en.Cliente)
		}
		if len(en.Sensible) > 0 {
			fmt.Fprintf(&b, " · sensible: %s", strings.Join(en.Sensible, ", "))
		}
		b.WriteString("\n")
	}
	b.WriteString("\neventos:\n")
	for _, ev := range t.LoadEvents() {
		fmt.Fprintf(&b, "  %s  %-14s %-7s %s\n", ev.TS.Format("02/01 15:04"), ev.Paso, ev.Estado, ev.Mensaje)
	}
	if fs, err := os.ReadDir(t.Dir); err == nil {
		var nombres []string
		for _, f := range fs {
			if !f.IsDir() {
				nombres = append(nombres, f.Name())
			}
		}
		sort.Strings(nombres)
		fmt.Fprintf(&b, "\narchivos en %s:\n  %s\n", t.Dir, strings.Join(nombres, "\n  "))
	}
	return b.String(), nil
}

// Limpiar saca los worktrees de las tareas cerradas (descartadas, mergeadas, o con PR abierta hace
// más de una semana). Las ramas quedan en el clon: la PR ya tiene el código.
func (c Config) Limpiar(ctx context.Context) ([]string, error) {
	ts, err := LoadTasks(c.Runs)
	if err != nil {
		return nil, err
	}
	var hechas []string
	for _, t := range ts {
		if !cerrada(t) {
			continue
		}
		dirs, _ := filepath.Glob(filepath.Join(t.Dir, "wt", "*"))
		for _, wt := range dirs {
			clone := filepath.Join(c.Home, "clones", filepath.Base(wt))
			if _, err := git(ctx, clone, "worktree", "remove", "--force", wt); err != nil {
				return hechas, fmt.Errorf("%s: %w", wt, err)
			}
			hechas = append(hechas, wt)
		}
	}
	return hechas, nil
}

const viejaPR = 7 * 24 * time.Hour

func cerrada(t *Task) bool {
	switch t.Est.Estado {
	case Descartada, Integrada:
		return true
	case PRAbierto, CIVerde, CIRojo:
		creada, err := time.ParseInLocation("20060102-1504", t.Est.ID[:13], time.Local)
		return err == nil && time.Since(creada) > viejaPR
	}
	return false
}

// MergeadoLocal registra que el operador trajo una tarea al main local de un proyecto sin GitHub (sin
// aprobación ni PR). Como DESCARTADA, la página la mostraría como «falló».
// Verifica, repo por repo de la spec, que la punta de factory/<id> ya esté en el main local.
func (c Config) MergeadoLocal(ctx context.Context, id string) error {
	t, err := findTask(c.Runs, id)
	if err != nil {
		return err
	}
	switch t.Est.Estado {
	case EsperandoAprobacion, Trabada, Descartada:
	default:
		return fmt.Errorf("%s está %s: solo se marca una tarea que espera la aprobación, trabada o cerrada", id, t.Est.Estado)
	}
	var spec Spec
	if err := t.ReadJSON("01-spec.json", &spec); err != nil || len(spec.Repos) == 0 {
		return fmt.Errorf("%s no tiene spec con repos", id)
	}
	cs := Clientes(c.Home)
	for _, nombre := range spec.Repos {
		rc, _, ok := buscarRepo(cs, nombre)
		if !ok {
			return fmt.Errorf("el repo %q no es de ningún proyecto", nombre)
		}
		punta, err := git(ctx, filepath.Join(c.Home, "clones", nombre), "rev-parse", "--verify", Branch(id))
		if err != nil {
			return fmt.Errorf("%s: no está la rama %s en el clon", nombre, Branch(id))
		}
		if _, err := git(ctx, rc.Ruta, "merge-base", "--is-ancestor", strings.TrimSpace(punta), "main"); err != nil {
			return fmt.Errorf("%s: la punta de %s (%s) no está en el main local", nombre, Branch(id), strings.TrimSpace(punta)[:7])
		}
	}
	return SetEstado(c.Runs, id, t.Est.Estado, Integrada, "integrada en el main local (proyecto sin GitHub)")
}
