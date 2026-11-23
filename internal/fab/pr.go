package fab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Después de la aprobación: la fábrica sigue la PR (CI y merge) leyendo gh. Lo único que escribe es el re-run
// de un CI rojo y el merge automático (automerge.go).
const (
	CIVerde   = "CI_VERDE"
	CIRojo    = "CI_ROJO"
	Integrada = "INTEGRADA"
)

// PRsArchivo guarda repo → URL de la PR: la aprobación lo escribe, el seguimiento lo lee.
const PRsArchivo = "prs.json"

// tituloPR: el tipo de la spec manda el prefijo (convención de commits).
func tituloPR(s Spec) string {
	if s.Tipo == "bug" {
		return "fix: " + s.Titulo
	}
	return "feat: " + s.Titulo
}

// cuerpoPR: lo que el operador necesita para revisar sin abrir la fábrica: la spec, qué test prueba
// cada criterio, lo que hizo el Builder, lo que los revisores dejaron a la vista y el costo.
func cuerpoPR(t *Task, s Spec) string {
	var b strings.Builder
	b.WriteString(s.SpecMD)
	b.WriteString(decisionesMD(s))
	var build Build
	if t.ReadJSON(fmt.Sprintf("02-build-v%d.json", t.Est.Vuelta), &build) == nil {
		if len(build.TestsAceptacion) > 0 {
			b.WriteString("\n\n## Criterios y sus tests\n\n")
			for _, ta := range build.TestsAceptacion {
				fmt.Fprintf(&b, "- %s: `%s` en `%s/%s`\n", ta.CA, ta.Test, ta.Repo, ta.Archivo)
			}
		}
		if build.Resumen != "" {
			b.WriteString("\n## Qué hizo el Builder\n\n" + build.Resumen + "\n")
		}
	}
	var d Decision
	if t.ReadJSON(fmt.Sprintf("04-approver-v%d.json", t.Est.Vuelta), &d) == nil {
		lista := func(titulo string, hs []Hallazgo) {
			if len(hs) == 0 {
				return
			}
			fmt.Fprintf(&b, "\n## %s\n\n", titulo)
			for _, h := range hs {
				fmt.Fprintf(&b, "- %s %s `%s/%s:%d` (%s): %s\n", h.Severidad, h.Regla, h.Repo, h.Archivo, h.Linea, h.Revisor, h.Detalle)
			}
		}
		lista("A confirmar antes del merge (graves sin evidencia)", d.AConfirmar)
		var fuera, resto []Hallazgo
		for _, h := range d.Descartado {
			if h.Regla == "fuera-de-alcance" {
				fuera = append(fuera, h)
			} else {
				resto = append(resto, h)
			}
		}
		lista("Fuera de alcance", fuera)
		lista("Descartados por el Approver (bajo el umbral)", resto)
		if len(d.SinRevisar) > 0 {
			b.WriteString("\n## Sin revisar\n\n- " + strings.Join(d.SinRevisar, "\n- ") + "\n")
		}
	}
	var en Entrada
	if t.ReadJSON("00-entrada.json", &en) == nil && len(en.Sensible) > 0 {
		fmt.Fprintf(&b, "\n**Sensible** (%s): conviene probarlo a mano en el entorno antes de darlo por terminado.\n", strings.Join(en.Sensible, ", "))
	}
	fmt.Fprintf(&b, "\n---\nHecha por ticket-factory, tarea `%s`, %d vuelta(s), US$%.2f estimados.\n", t.Est.ID, t.Est.Vuelta, t.Est.CostUSD)
	return b.String()
}

// prExistente: la URL de la PR abierta de esa rama, si ya hay una (la aprobación es idempotente).
func prExistente(ctx context.Context, slug, rama string) string {
	out, err := ghSalida(ctx, "pr", "list", "--repo", slug, "--head", rama, "--state", "open",
		"--json", "url", "--jq", ".[0].url")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// estadoPR lee la PR: MERGED, o el resultado de sus checks.
type estadoPR struct {
	State  string    `json:"state"`
	Checks []checkPR `json:"statusCheckRollup"`
}

// checkPR es un check de la PR (un job de Actions o un status).
type checkPR struct {
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
	Status     string `json:"status"`
	DetailsURL string `json:"detailsUrl"`
}

// estadoDePRs resume varias PRs (una por repo) en un estado de la tarea; "" si nada cambió todavía.
func estadoDePRs(prs []estadoPR) string {
	mergeadas, verdes := 0, 0
	for _, p := range prs {
		if p.State == "MERGED" {
			mergeadas++
			continue
		}
		if p.State == "CLOSED" {
			return Descartada // la cerraron sin merge en GitHub
		}
		pendiente, rojo := len(p.Checks) == 0, false
		for _, c := range p.Checks {
			v := strings.ToUpper(c.Conclusion + c.State)
			switch {
			case strings.Contains(v, "FAILURE"), strings.Contains(v, "ERROR"), strings.Contains(v, "CANCELLED"), strings.Contains(v, "TIMED_OUT"),
				strings.Contains(v, "ACTION_REQUIRED"), strings.Contains(v, "STALE"):
				rojo = true
			case v == "" || strings.Contains(v, "PENDING") || c.Status == "IN_PROGRESS" || c.Status == "QUEUED":
				pendiente = true
			}
		}
		if rojo {
			return CIRojo
		}
		if !pendiente {
			verdes++
		}
	}
	switch {
	case len(prs) > 0 && mergeadas == len(prs):
		return Integrada
	case len(prs) > 0 && mergeadas+verdes == len(prs):
		return CIVerde
	}
	return ""
}

// SeguirPRs actualiza las tareas con PR según lo que dice GitHub, y mergea sola la que puede.
func (c Config) SeguirPRs(ctx context.Context) []string {
	ts, err := LoadTasks(c.Runs)
	if err != nil {
		return []string{err.Error()}
	}
	var log []string
	for _, t := range ts {
		if t.Est.Estado != PRAbierto && t.Est.Estado != CIVerde && t.Est.Estado != CIRojo {
			continue
		}
		var urls map[string]string
		if t.ReadJSON(PRsArchivo, &urls) != nil || len(urls) == 0 {
			continue
		}
		var spec Spec
		_ = t.ReadJSON("01-spec.json", &spec)
		cs := Clientes(c.Home)
		var prs []estadoPR
		for repo, u := range urls {
			out, err := ghSalida(ctx, "pr", "view", u, "--json", "state,statusCheckRollup")
			var p estadoPR
			if err != nil || json.Unmarshal(out, &p) != nil {
				prs = nil
				break
			}
			if rc, _, ok := buscarRepo(cs, repo); ok && rc.CIManual && p.State == "OPEN" && len(p.Checks) == 0 {
				p.Checks = checksDeCIManual(ctx, u, Branch(t.Est.ID))
			}
			prs = append(prs, p)
		}
		nuevo := estadoDePRs(prs)
		if nuevo == CIRojo && (hayChecksCorriendo(prs) || reintentoReciente(t)) {
			continue // que termine la corrida (o la que se acaba de relanzar) antes de decidir
		}
		if nuevo == CIRojo && reintentarCI(ctx, t, prs) {
			log = append(log, t.Est.ID+": CI rojo, re-corro una vez los jobs que fallaron (puede ser un test inestable)")
			continue
		}
		if nuevo == Integrada {
			nuevo = estadoTrasMerge(t) // una app suelta queda pendiente de deploy (deploy.go)
		}
		if nuevo != "" && nuevo != t.Est.Estado {
			if err := t.Transicion(t.Est.Estado, nuevo, "según GitHub"); err == nil {
				log = append(log, t.Est.ID+" → "+nuevo)
				if nuevo == Integrada {
					marcarNotificacionPendiente(t) // la despliega el trigger del repo; se redacta aparte
				}
			}
		}
		if t.Est.Estado == CIVerde {
			if l := c.autoMerge(ctx, t, urls); l != "" {
				log = append(log, l)
			}
		}
	}
	return log
}

// prURL: el repo (owner/repo) de la URL de una PR.
var prURL = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/\d+`)

// salidaGH es `gh` con su salida (variable para los tests).
var salidaGH = ghSalida

// checksDeCIManual: en los repos con ci_manual la corrida que dispara la aprobación es un
// workflow_dispatch y GitHub no la cuelga de la PR: statusCheckRollup viene vacío y la tarea quedaría
// en PR_ABIERTO para siempre. Se lee la última corrida de workflowCIManual en la rama de la tarea,
// con la misma forma que un check.
func checksDeCIManual(ctx context.Context, url, rama string) []checkPR {
	m := prURL.FindStringSubmatch(url)
	if m == nil {
		return nil
	}
	out, err := salidaGH(ctx, "run", "list", "--repo", m[1], "--branch", rama, "--workflow", workflowCIManual,
		"--event", "workflow_dispatch", "--limit", "1", "--json", "status,conclusion,url")
	var runs []struct{ Status, Conclusion, URL string }
	if err != nil || json.Unmarshal(out, &runs) != nil || len(runs) == 0 {
		return nil
	}
	r := runs[0]
	return []checkPR{{Conclusion: strings.ToUpper(r.Conclusion), Status: strings.ToUpper(r.Status), DetailsURL: r.URL}}
}

// Un CI rojo se re-corre una vez antes de darlo por rojo: un test inestable del repo no deja la tarea
// en rojo si pasa al re-correr.
const ciReintentado = "ci-reintento.json"

var reRun = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/actions/runs/(\d+)(/|$)`)

// correrGH es `gh` (variable para los tests).
var correrGH = func(ctx context.Context, args ...string) error {
	_, err := ghSalida(ctx, args...)
	return err
}

// runsFallidos: repo y run de cada check en rojo (sin repetir).
func runsFallidos(prs []estadoPR) [][2]string {
	visto := map[string]bool{}
	var runs [][2]string
	for _, p := range prs {
		for _, c := range p.Checks {
			v := strings.ToUpper(c.Conclusion + c.State)
			if !strings.Contains(v, "FAILURE") && !strings.Contains(v, "ERROR") && !strings.Contains(v, "TIMED_OUT") {
				continue
			}
			if m := reRun.FindStringSubmatch(c.DetailsURL); m != nil && !visto[m[2]] {
				visto[m[2]] = true
				runs = append(runs, [2]string{m[1], m[2]})
			}
		}
	}
	return runs
}

// reintentarCI re-corre los jobs fallidos la primera vez que la tarea ve el CI en rojo. Devuelve true
// si lo hizo (la tarea sigue como está hasta que termine la nueva corrida).
func reintentarCI(ctx context.Context, t *Task, prs []estadoPR) bool {
	var hecho []string
	if t.ReadJSON(ciReintentado, &hecho) == nil {
		return false
	}
	runs := runsFallidos(prs)
	if len(runs) == 0 {
		return false
	}
	for _, r := range runs {
		if err := correrGH(ctx, "run", "rerun", r[1], "--failed", "--repo", r[0]); err == nil {
			hecho = append(hecho, r[0]+"#"+r[1])
		}
	}
	if len(hecho) == 0 {
		return false
	}
	_ = t.WriteJSON(ciReintentado, hecho)
	t.Log("08-ci", "reintento", "re-corro los jobs que fallaron: "+strings.Join(hecho, ", "), 0)
	return true
}

// hayChecksCorriendo: algún job de la PR todavía no terminó.
func hayChecksCorriendo(prs []estadoPR) bool {
	for _, p := range prs {
		for _, c := range p.Checks {
			v := strings.ToUpper(c.Conclusion + c.State + c.Status)
			if strings.Contains(v, "PENDING") || strings.Contains(v, "IN_PROGRESS") || strings.Contains(v, "QUEUED") ||
				(c.Conclusion == "" && c.State == "" && c.Status != "COMPLETED") {
				return true
			}
		}
	}
	return false
}

// reintentoReciente: se relanzó el CI hace menos de unos minutos; GitHub puede seguir mostrando el
// rojo viejo hasta que arranca la corrida nueva.
func reintentoReciente(t *Task) bool {
	fi, err := os.Stat(filepath.Join(t.Dir, ciReintentado))
	return err == nil && time.Since(fi.ModTime()) < 3*time.Minute
}

// URLAbrible: lo único que el panel abre en el navegador (M1). La URL exacta de una PR que la fábrica
// guardó en el prs.json de alguna tarea, o una página de la vista previa registrada en preview.json
// (mismo esquema y host:puerto). Nada que el pedido traiga por su cuenta.
func URLAbrible(runs, s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.User != nil || s == "" {
		return false
	}
	if p := LoadPreview(runs); p != nil && p.URL != "" {
		if v, err := url.Parse(p.URL); err == nil && u.Scheme == v.Scheme && u.Host == v.Host && u.Scheme == "http" {
			return true
		}
	}
	ts, err := LoadTasks(runs)
	if err != nil {
		return false
	}
	for _, t := range ts {
		var urls map[string]string
		if t.ReadJSON(PRsArchivo, &urls) != nil {
			continue
		}
		for _, pr := range urls {
			if pr == s {
				return true
			}
		}
	}
	return false
}
