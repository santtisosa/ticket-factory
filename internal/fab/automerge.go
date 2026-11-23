package fab

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Merge automático: con merge.automatico activo, una tarea con el check requerido en verde se integra
// sola si no tiene nada que revisar a mano. Ante cualquier duda no integra y la tarea queda en
// CI_VERDE para una persona. Se intenta una sola vez.
const autoMergeArchivo = "auto-merge.json"

// sensiblesFijos: lo que siempre cuenta como sensible, además de merge.sensibles: el CI, las
// migraciones y los manifiestos de dependencias. Un patrón sin "/" se compara con el nombre del
// archivo a cualquier profundidad (web/package.json).
// También lo que corre al construir o probar (Makefile, Dockerfile, compose, hooks) y la configuración
// de npm, que decide de dónde salen las dependencias.
var sensiblesFijos = []string{".github/**", "**/migrations/**", "**/migraciones/**",
	"package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", ".npmrc", "go.mod", "go.sum",
	"Makefile", "Dockerfile*", "docker-compose*", "compose*.yml", "compose*.yaml", "hooks/**", "**/hooks/**",
	// Revisión del 22/11: el despliegue de Cloud Build, la config de las herramientas de build y test
	// (el matcher no tiene llaves: un patrón por extensión), lo que cambia cómo git trata los archivos
	// o trae otros repos, los scripts y lo que Claude Code carga de un directorio.
	"cloudbuild*.yml", "cloudbuild*.yaml", "*.config.js", "*.config.ts", "*.config.mjs", "*.config.cjs",
	".gitattributes", ".gitmodules", "scripts/**", "**/scripts/**", ".claude/**", "**/.claude/**"}

// patronSensible devuelve el primer patrón (fijo o de merge.sensibles) que cubre la ruta, o "". Sin
// distinguir mayúsculas (makefile, DOCKERFILE). Un patrón mal escrito cuenta como coincidencia: ante
// la duda, no se integra solo.
// ponytail: en un rename, `gh pr view --json files` trae solo la ruta nueva; un archivo sensible
// movido a un nombre que no lo parece (Makefile a docs/x.txt) no se detecta. Leer la API de archivos
// de la PR (previous_filename) si importa.
func patronSensible(ruta string, extra []string) string {
	ruta = strings.ToLower(ruta)
	for _, p := range append(append([]string{}, sensiblesFijos...), extra...) {
		if ok, err := coincideGlob(strings.ToLower(p), ruta); ok || err != nil {
			return p
		}
	}
	return ""
}

// coincideGlob es path.Match por segmentos, donde un segmento "**" cubre cero o más carpetas. Sin
// "/" en el patrón, se compara con el nombre del archivo.
func coincideGlob(patron, ruta string) (bool, error) {
	if !strings.Contains(patron, "/") {
		return path.Match(patron, path.Base(ruta))
	}
	return coincideSegs(strings.Split(patron, "/"), strings.Split(ruta, "/"))
}

// globValido: cada segmento del patrón es un patrón válido de path.Match.
func globValido(patron string) bool {
	if strings.TrimSpace(patron) == "" {
		return false
	}
	for _, seg := range strings.Split(patron, "/") {
		if _, err := path.Match(seg, ""); err != nil {
			return false
		}
	}
	return true
}

func coincideSegs(p, r []string) (bool, error) {
	if len(p) == 0 {
		return len(r) == 0, nil
	}
	if p[0] == "**" {
		for i := 0; i <= len(r); i++ {
			if ok, err := coincideSegs(p[1:], r[i:]); ok || err != nil {
				return ok, err
			}
		}
		return false, nil
	}
	if len(r) == 0 {
		return false, nil
	}
	ok, err := path.Match(p[0], r[0])
	if !ok || err != nil {
		return false, err
	}
	return coincideSegs(p[1:], r[1:])
}

// reURLPR: owner/repo y número de una PR de GitHub.
var reURLPR = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/(\d+)$`)

// checkDeMerge es una entrada de statusCheckRollup: un CheckRun (name, status, conclusion) o un
// StatusContext (context, state).
type checkDeMerge struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Context    string `json:"context"`
	State      string `json:"state"`
}

func (c checkDeMerge) nombre() string { return c.Name + c.Context }
func (c checkDeMerge) verde() bool {
	return c.Conclusion == "SUCCESS" || c.State == "SUCCESS"
}

// terminado: un CheckRun completo o un StatusContext que ya no está pendiente.
func (c checkDeMerge) terminado() bool {
	if c.Context != "" || c.State != "" {
		return c.State != "PENDING" && c.State != "EXPECTED"
	}
	return c.Status == "COMPLETED"
}

// aceptable: terminó sin fallar (un check omitido o neutral no frena).
func (c checkDeMerge) aceptable() bool {
	switch c.Conclusion + c.State {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return true
	}
	return false
}

// prParaMerge es lo que se lee de la PR antes de mergear.
type prParaMerge struct {
	Number       int    `json:"number"`
	Mergeable    string `json:"mergeable"`
	HeadRefOid   string `json:"headRefOid"`
	HeadRefName  string `json:"headRefName"`
	ChangedFiles int    `json:"changedFiles"`
	Files        []struct {
		Path string `json:"path"`
	} `json:"files"`
	Checks []checkDeMerge `json:"statusCheckRollup"`
}

// guardiaMerge: "" si la PR se puede mergear según sus checks y su head; si no, por qué no. El check
// requerido tiene que estar presente y en verde (todas sus entradas, si hay varias), ningún otro
// check puede estar rojo o corriendo, y el head tiene que ser el commit aprobado.
func guardiaMerge(pr prParaMerge, requerido, aprobado string) string {
	if aprobado == "" || pr.HeadRefOid != aprobado {
		return "la rama no es el commit aprobado (hubo cambios después de la aprobación)"
	}
	visto := false
	for _, c := range pr.Checks {
		if c.nombre() == requerido {
			if !c.verde() {
				return requerido + " no está en verde"
			}
			visto = true
			continue
		}
		if !c.terminado() {
			return "el check " + c.nombre() + " todavía corre"
		}
		if !c.aceptable() {
			return "el check " + c.nombre() + " no está en verde"
		}
	}
	if !visto {
		return requerido + " no está en verde (no aparece en la PR)"
	}
	return ""
}

// motivoSinAutoMerge: "" si la tarea se puede mergear sola; si no, por qué no (queda para una persona).
func motivoSinAutoMerge(id string, d Decision, urls map[string]string, aprobado map[string]string, pr prParaMerge, conDeploy map[string]bool, m Merge) string {
	switch {
	case len(urls) != 1:
		return "toca más de un repo (el orden del merge lo decide una persona)"
	case len(d.AConfirmar) > 0:
		return "tiene hallazgos graves a confirmar"
	case len(d.SinRevisar) > 0:
		return "quedó algo sin revisar"
	case pr.Mergeable != "MERGEABLE":
		return "GitHub no la da por mergeable (" + pr.Mergeable + ")"
	case len(pr.Files) == 0 || len(pr.Files) != pr.ChangedFiles:
		return "no se pudo leer todos los archivos de la PR"
	case pr.HeadRefName != Branch(id):
		return "la PR no es de la rama de la tarea"
	}
	for repo := range urls {
		// Integrar un repo con deploy lo despliega (deploy.go): eso lo decide una persona.
		if conDeploy[repo] {
			return repo + " se despliega al integrar: el merge queda para una persona"
		}
		if motivo := guardiaMerge(pr, m.CheckRequerido, aprobado[repo]); motivo != "" {
			return motivo
		}
	}
	for _, f := range pr.Files {
		if p := patronSensible(f.Path, m.Sensibles); p != "" {
			return "toca " + f.Path + " (sensible: " + p + ")"
		}
	}
	return ""
}

// leerGH es `gh` con su salida (variable para los tests).
var leerGH = ghSalida

// autoMerge intenta una vez mergear la PR de una tarea en CI_VERDE. Devuelve una línea para el log.
// Con merge.automatico apagado (o sin un fabrica.yaml legible) no hace nada ni deja marca.
func (c Config) autoMerge(ctx context.Context, t *Task, urls map[string]string) string {
	e, err := CargarEspacio(c.Home)
	if err != nil || !e.Merge.Automatico {
		return ""
	}
	var hecho string
	if t.ReadJSON(autoMergeArchivo, &hecho) == nil {
		return ""
	}
	motivo := c.mergear(ctx, t, urls, e.Merge)
	_ = t.WriteJSON(autoMergeArchivo, motivo)
	if motivo == "" {
		t.Log("08-merge", "ok", "integrada sola (check requerido en verde, nada que revisar a mano)", 0)
		return t.Est.ID + ": integrada sola"
	}
	t.Log("08-merge", "manual", motivo, 0)
	return t.Est.ID + ": sin merge automático: " + motivo
}

func (c Config) mergear(ctx context.Context, t *Task, urls map[string]string, m Merge) string {
	var d Decision
	var aprobado map[string]string
	if t.ReadJSON(fmt.Sprintf("04-approver-v%d.json", t.Est.Vuelta), &d) != nil || t.ReadJSON("aprobado.json", &aprobado) != nil {
		return "no se pudo leer la decisión o lo aprobado"
	}
	var repo, url string
	for r, u := range urls {
		repo, url = r, u
	}
	partes := reURLPR.FindStringSubmatch(url)
	if partes == nil {
		return "la URL no es de una PR de GitHub: " + url
	}
	var pr prParaMerge
	out, err := leerGH(ctx, "pr", "view", url, "--json", "number,mergeable,headRefOid,headRefName,changedFiles,files,statusCheckRollup")
	if err != nil || json.Unmarshal(out, &pr) != nil {
		return fmt.Sprintf("no se pudo leer la PR (%v)", err)
	}
	conDeploy := map[string]bool{}
	for _, n := range appsDe(t) {
		conDeploy[n] = true
	}
	if motivo := motivoSinAutoMerge(t.Est.ID, d, urls, aprobado, pr, conDeploy, m); motivo != "" {
		return motivo
	}
	// --match-head-commit: si alguien subió algo entre la lectura y el merge, GitHub no mergea.
	if _, err := leerGH(ctx, "pr", "merge", partes[2], "--repo", partes[1], "--squash", "--match-head-commit", aprobado[repo]); err != nil {
		return "gh pr merge falló: " + err.Error()
	}
	return ""
}
