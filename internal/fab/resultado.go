package fab

// El revisor del resultado: en las tareas que producen algo que el usuario final ve (un PDF, un correo,
// una pantalla), un revisor con otro modelo genera el resultado real con datos de borde y lo mide. Un
// generador de PDF puede pasar la revisión de código sin hallazgos y fallar recién al ejecutarlo.

import (
	"context"
	"path/filepath"
	"strings"
)

// extVisibles: archivos que dibujan lo que ve el cliente.
var extVisibles = map[string]bool{".html": true, ".htm": true, ".tsx": true, ".jsx": true, ".vue": true,
	".svelte": true, ".css": true, ".scss": true}

// palabrasVisibles: archivos que arman algo que se imprime o se manda, por su nombre.
var palabrasVisibles = []string{"pdf", "mail", "correo", "smtp", "plantilla", "template", "etiqueta", "imprim"}

// esVisible: el archivo dibuja, imprime o manda algo que ve el cliente. Los tests no cuentan.
func esVisible(ruta string) bool {
	base := strings.ToLower(filepath.Base(ruta))
	if strings.Contains(base, ".test.") || strings.HasSuffix(base, "_test.go") || strings.Contains(ruta, "/test/") {
		return false
	}
	if extVisibles[filepath.Ext(base)] {
		return true
	}
	for _, p := range palabrasVisibles {
		if strings.Contains(base, p) {
			return true
		}
	}
	return false
}

// produceVisible: la rama tocó algo visible en algún repo (se decide por el diff, no por la spec).
func produceVisible(ctx context.Context, repos []Repo) bool {
	for _, r := range repos {
		cambiados, err := ChangedSince(ctx, r, baseDe(r))
		if err != nil {
			continue
		}
		for _, f := range cambiados {
			if esVisible(f) {
				return true
			}
		}
	}
	return false
}

// vieneLimpia: los otros revisores entregaron y ninguno frenó con algo grave. Si no, la vuelta ya no
// pasa y ejecutar sería gastar en un código que va a cambiar.
func vieneLimpia(reviews []Review) bool {
	for _, r := range reviews {
		if r.Err != "" && !strings.HasPrefix(r.Err, infraPrefijo) {
			return false
		}
		for _, h := range r.Hallazgos {
			if h.Severidad == "HIGH" || h.Severidad == "CRITICAL" {
				return false
			}
		}
	}
	return true
}

// revisarResultado corre el revisor y después deja los worktrees en el commit que revisó: lo que haya
// escrito o commiteado no llega a la aprobación. Si se cae, queda "sin revisar" (no cuesta una vuelta al
// Builder) y la tarea no se mergea sola.
func (c Config) revisarResultado(ctx context.Context, t *Task, spec Spec, repos []Repo, herramientas string) Review {
	heads, ramas := map[string]string{}, map[string]string{}
	for _, r := range repos {
		if r.HeadSHA != "" {
			heads[r.WT] = r.HeadSHA // vuelve al commit fijado para la revisión, no al que encuentre
		} else if sha, err := git(ctx, r.WT, "rev-parse", "HEAD"); err == nil {
			heads[r.WT] = sha
		}
		if rama, err := git(ctx, r.WT, "symbolic-ref", "--short", "HEAD"); err == nil {
			ramas[r.WT] = rama
		}
	}
	settings, err := c.settingsDeTarea(t.Dir, "resultado", repos)
	if err != nil {
		return Review{Revisor: "resultado", Err: infraPrefijo + "settings del revisor del resultado: " + err.Error()}
	}
	rv := reviewer{name: "resultado", settings: settings,
		tools: []string{"Read", "Grep", "Glob", "Bash", "StructuredOutput"}} // sin Write: corre fuera del sandbox
	out := c.reviewStage(ctx, t, spec, repos, []reviewer{rv}, herramientas)
	for _, r := range repos {
		restaurar(ctx, t, r, ramas[r.WT], heads[r.WT])
	}
	r := out[0]
	if r.Err != "" && !strings.HasPrefix(r.Err, infraPrefijo) {
		r.Err = infraPrefijo + r.Err
	}
	return r
}

// gitSinHooks: git sobre un worktree que tocó un agente, sin correr sus hooks ni su fsmonitor (un hook
// escrito por el agente correría afuera del sandbox, con las credenciales del operador).
func gitSinHooks(ctx context.Context, dir string, args ...string) (string, error) {
	return git(ctx, dir, append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)...)
}

// restaurar vuelve el worktree a su rama y al commit revisado: saca otra rama, commits nuevos, cambios
// y archivos sueltos (los ignorados, como node_modules, quedan).
func restaurar(ctx context.Context, t *Task, r Repo, rama, sha string) {
	if rama != "" {
		if actual, err := gitSinHooks(ctx, r.WT, "symbolic-ref", "--short", "HEAD"); err != nil || actual != rama {
			t.Log("04-resultado", "falla", r.Name+": el revisor cambió de rama; vuelve a "+rama, 0)
			if _, err := gitSinHooks(ctx, r.WT, "checkout", "-f", rama); err != nil {
				t.Log("04-resultado", "falla", r.Name+": no se pudo volver a la rama: "+err.Error(), 0)
			}
		}
	}
	if sha != "" {
		if actual, err := gitSinHooks(ctx, r.WT, "rev-parse", "HEAD"); err == nil && actual != sha {
			t.Log("04-resultado", "falla", r.Name+": el revisor movió la rama; vuelve a "+sha[:min(8, len(sha))], 0)
			if _, err := gitSinHooks(ctx, r.WT, "reset", "--hard", sha); err != nil {
				t.Log("04-resultado", "falla", r.Name+": no se pudo volver la rama: "+err.Error(), 0)
			}
		}
	}
	if _, err := gitSinHooks(ctx, r.WT, "checkout", "--", "."); err != nil {
		t.Log("04-resultado", "falla", r.Name+": no se pudo descartar cambios: "+err.Error(), 0)
	}
	if _, err := gitSinHooks(ctx, r.WT, "clean", "-fdq"); err != nil {
		t.Log("04-resultado", "falla", r.Name+": no se pudo borrar archivos sueltos: "+err.Error(), 0)
	}
}
