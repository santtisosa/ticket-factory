package fab

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// La vista previa: cuando una tarea espera la aprobación, la línea deja la app de la rama levantada
// con la receta `preview` del proyecto, en un puerto libre de 127.0.0.1, para mirarla antes de
// aceptar. Una sola a la vez: levantar otra baja la anterior.

// PreviewVida: la vista previa se apaga sola a los 20 min; la página deja un botón para volver a
// levantarla. Así no queda una app corriendo toda la noche por una tarea que nadie mira.
const PreviewVida = 20 * time.Minute

// Preview es runs/.cache/preview.json: lo que está levantado y cómo bajarlo desde cualquier proceso
// (la línea la levanta; la aprobación, cancelar o la terminal la bajan). Nunca lleva credenciales.
type Preview struct {
	ID    string    `json:"id"`
	URL   string    `json:"url,omitempty"`
	Rutas []string  `json:"rutas,omitempty"`
	Error string    `json:"error,omitempty"`
	Desde time.Time `json:"desde"`
	Hasta time.Time `json:"hasta,omitzero"` // cuándo se apaga sola
	// Levantando: la está arrancando alguien (la línea o el botón). Apagada: venció; se vuelve a levantar.
	Levantando bool      `json:"levantando,omitempty"`
	Apagada    bool      `json:"apagada,omitempty"`
	Procesos   []Proceso `json:"procesos,omitempty"` // los grupos de los comandos de la receta
}

var previewMu sync.Mutex

func previewPath(runs string) string { return filepath.Join(runs, ".cache", "preview.json") }

// LoadPreview devuelve la vista previa que haya, o nil.
func LoadPreview(runs string) *Preview {
	b, err := os.ReadFile(previewPath(runs))
	if err != nil {
		return nil
	}
	var p Preview
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	return &p
}

func savePreview(runs string, p *Preview) error {
	b, err := json.MarshalIndent(p, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(previewPath(runs), b, 0o600)
}

// StopPreview baja la vista previa que haya y borra su registro.
func StopPreview(runs string) {
	if p := LoadPreview(runs); p != nil {
		bajarProcesos(p.Procesos)
	}
	_ = os.Remove(previewPath(runs))
}

// StopPreviewDe baja la vista previa solo si es de esa tarea (la aprobación o cancelar de otra no la tocan).
func StopPreviewDe(runs, id string) {
	if p := LoadPreview(runs); p != nil && p.ID == id {
		StopPreview(runs)
	}
}

// ReconcilePreview, al arrancar la línea: si la tarea ya no espera la aprobación, su vista previa sobra.
func ReconcilePreview(runs string) {
	p := LoadPreview(runs)
	if p == nil {
		return
	}
	if t, err := findTask(runs, p.ID); err != nil || t.Est.Estado != EsperandoAprobacion {
		StopPreview(runs)
		return
	}
	// Después de reiniciar la máquina los procesos ya no están: que la página no muestre una URL muerta.
	if len(p.Procesos) > 0 && !slices.ContainsFunc(p.Procesos, grupoNuestro) {
		p.Procesos, p.URL = nil, ""
		p.Error = "la app ya no está corriendo (¿se reinició la máquina?); para levantarla de nuevo: fabrica preview " + p.ID
		_ = savePreview(runs, p)
	}
}

// VencerPreview apaga la vista previa que pasó su vida y la deja marcada "apagada", para que la
// página ofrezca levantarla de nuevo. La llaman la línea y la página cada tanto.
func VencerPreview(runs string) {
	p := LoadPreview(runs)
	if p == nil || p.Apagada {
		return
	}
	// Un arranque que se cortó (la línea se reinició a mitad) no puede trabar el botón para siempre.
	if p.Levantando && time.Since(p.Desde) > appUpWait+2*time.Minute {
		bajarProcesos(p.Procesos)
		p.Procesos, p.Levantando = nil, false
		p.Error = "se interrumpió mientras se levantaba; se puede volver a intentar"
		_ = savePreview(runs, p)
		return
	}
	if p.Levantando || p.Hasta.IsZero() || time.Now().Before(p.Hasta) {
		return
	}
	bajarProcesos(p.Procesos)
	p.Procesos, p.Apagada = nil, true
	_ = savePreview(runs, p)
}

// VencerPreviews corre VencerPreview cada 30 s hasta que termine ctx.
func VencerPreviews(ctx context.Context, runs string) {
	tk := time.NewTicker(30 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			VencerPreview(runs)
		}
	}
}

// PuedePreview: solo una tarea que espera la aprobación tiene vista previa.
func PuedePreview(runs, id string) error {
	t, err := findTask(runs, id)
	if err != nil {
		return err
	}
	if t.Est.Estado != EsperandoAprobacion {
		return fmt.Errorf("%s está en %s: la vista previa es para una tarea que espera la aprobación", id, t.Est.Estado)
	}
	if p := LoadPreview(runs); p != nil && p.Levantando {
		return fmt.Errorf("ya se está levantando la vista previa de %s", p.ID)
	}
	return nil
}

// StartPreview baja la anterior y levanta la de esta tarea. Si no levanta, la tarea igual espera la
// aprobación: el panel muestra el motivo y `fabrica preview <id>` lo vuelve a intentar.
func (c Config) StartPreview(ctx context.Context, t *Task) *Preview {
	var spec Spec
	if err := t.ReadJSON("01-spec.json", &spec); err != nil {
		t.Log("07-preview", "falla", "no se pudo leer la spec: "+err.Error(), 0)
		return &Preview{ID: t.Est.ID, Error: "no se pudo leer la spec: " + err.Error()}
	}
	// Un proyecto sin receta no baja la vista previa de otra tarea: solo lo anota.
	cl, receta, err := c.recetaDe(spec.Repos)
	if err != nil {
		t.Log("07-preview", "falla", err.Error(), 0)
		return &Preview{ID: t.Est.ID, Error: err.Error()}
	}
	// Con un carril por proyecto, dos tareas pueden llegar a la aprobación a la vez: se levanta de a una.
	previewMu.Lock()
	defer previewMu.Unlock()
	StopPreview(c.Runs)
	p := &Preview{ID: t.Est.ID, Desde: time.Now(), Levantando: true, Rutas: spec.Rutas}
	_ = savePreview(c.Runs, p) // la página muestra "levantando" mientras tanto
	t.Log("07-preview", "inicio", "levantando la app de la rama", 0)
	a, err := c.levantarPreview(ctx, t, spec, cl, *receta)
	if err != nil {
		p.Error = err.Error()
		t.Log("07-preview", "falla", err.Error(), 0)
	} else {
		p.URL, p.Procesos = a.URL, a.Procesos
		p.Desde = time.Now()
		p.Hasta = p.Desde.Add(PreviewVida)
		t.Log("07-preview", "ok", p.URL+" hasta las "+p.Hasta.Format("15:04"), 0)
	}
	p.Levantando = false
	if err := savePreview(c.Runs, p); err != nil {
		fmt.Fprintln(os.Stderr, "no se pudo guardar preview.json (no se va a poder bajar sola):", err)
	}
	return p
}

// levantarPreview: los worktrees del proyecto (la rama en los repos de la spec, main en los demás),
// los servicios de prueba y la receta.
func (c Config) levantarPreview(ctx context.Context, t *Task, spec Spec, cl Cliente, receta RecetaPreview) (*App, error) {
	repos, err := c.worktreesDeTarea(ctx, t, spec.Repos)
	if err != nil {
		return nil, fmt.Errorf("worktrees: %w", err)
	}
	repos = append(repos, c.hermanos(ctx, t, repos)...)
	perfil, err := c.prepararEncierro(ctx, t.Est.ID, t.Dir, repos)
	if err != nil {
		return nil, err
	}
	env, err := c.asegurarServicios(ctx, spec.Repos)
	if err != nil {
		return nil, fmt.Errorf("servicios de prueba: %w", err)
	}
	bajarModulos(ctx, perfil, dirsOf(repos)...)
	return arrancarApp(ctx, receta, repoApp(cl, repos).WT, env, filepath.Join(t.Dir, "preview.log"), perfil)
}

// worktreesDeTarea: los worktrees de la rama que ya dejó el pipeline, sin volver a traer el origin
// (la vista previa no tiene por qué depender de la red); los que falten se crean.
func (c Config) worktreesDeTarea(ctx context.Context, t *Task, names []string) ([]Repo, error) {
	var repos []Repo
	var nombres []string
	for _, n := range names {
		r := Repo{Name: n, Src: filepath.Join(c.Home, "clones", n), WT: filepath.Join(t.Dir, "wt", n)}
		if _, err := os.Stat(r.WT); err == nil {
			repos = append(repos, r)
		} else {
			nombres = append(nombres, n)
		}
	}
	faltan, err := c.worktrees(ctx, t, nombres)
	if err != nil {
		return nil, err
	}
	return append(repos, faltan...), nil
}

// PreviewDe levanta a mano la vista previa de una tarea que espera la aprobación (para cambiar de
// tarea o reintentar una que no levantó).
func (c Config) PreviewDe(ctx context.Context, id string) (*Preview, error) {
	if err := PuedePreview(c.Runs, id); err != nil {
		return nil, err
	}
	t, err := findTask(c.Runs, id)
	if err != nil {
		return nil, err
	}
	return c.StartPreview(ctx, t), nil
}

// Resumen es lo que se imprime en la terminal.
func (p *Preview) Resumen() string {
	if p.Error != "" {
		return fmt.Sprintf("vista previa de %s: no levantó: %s\n  para reintentar: fabrica preview %s\n", p.ID, p.Error, p.ID)
	}
	if p.Apagada {
		return fmt.Sprintf("vista previa de %s: apagada a los %v; para levantarla de nuevo: fabrica preview %s o el botón de la página\n", p.ID, PreviewVida, p.ID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "vista previa de %s: %s (se apaga a las %s)\n", p.ID, p.URL, p.Hasta.Format("15:04"))
	for _, r := range p.Rutas {
		fmt.Fprintf(&b, "  %s%s\n", p.URL, r)
	}
	return b.String()
}
