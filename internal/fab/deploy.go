package fab

// Despliegue posterior al merge. Un repo con `deploy:` en fabrica.yaml no tiene quien lo despliegue
// solo: una tarea que lo toca pasa de integrada a DESPLIEGUE_PENDIENTE y la línea corre su comando en
// su propia vuelta (fuera de la ingesta: un despliegue tarda minutos). Si el repo declara `smoke:`,
// esa URL tiene que responder sin error 5xx para darlo por bueno.
//
// Se despliega el main de GitHub, en un clon temporal nuevo y exportado limpio (git archive): nunca el
// checkout local (puede estar atrasado), ni el clon de la fábrica (escriben los agentes), ni un
// worktree. Y solo si ese main ya tiene el merge del pull request de la tarea: si no, desplegaría
// código sin el cambio y lo daría por bueno.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DesplieguePendiente: integrada, falta desplegar (solo repos con deploy).
const DesplieguePendiente = "DESPLIEGUE_PENDIENTE"

const (
	deployArchivo     = "08-deploy.json"
	deployTimeout     = 25 * time.Minute
	deployReintento   = 30 * time.Minute // entre intentos fallidos
	deployMaxIntentos = 3                // después queda TRABADA: `fabrica desplegar` la reintenta
)

// reposConDeploy: los repos de la spec de la tarea que declaran deploy en fabrica.yaml.
func reposConDeploy(t *Task) []RepoCliente {
	var spec Spec
	if t.ReadJSON("01-spec.json", &spec) != nil {
		return nil
	}
	cs := clientesDe(filepath.Dir(t.Dir))
	var out []RepoCliente
	for _, n := range spec.Repos {
		if rc, _, ok := buscarRepo(cs, n); ok && strings.TrimSpace(rc.Deploy) != "" {
			out = append(out, rc)
		}
	}
	return out
}

// appsDe: los nombres de los repos con deploy que tocó la tarea.
func appsDe(t *Task) []string {
	var out []string
	for _, rc := range reposConDeploy(t) {
		out = append(out, rc.Nombre)
	}
	return out
}

// DeployRepo es el último intento de un repo en una tarea.
type DeployRepo struct {
	SHA      string    `json:"sha,omitempty"`
	OK       bool      `json:"ok"`
	Detalle  string    `json:"detalle"`
	Intentos int       `json:"intentos"`
	Ultimo   time.Time `json:"ultimo"`
}

// estadoTrasMerge: una tarea integrada que tocó un repo con deploy queda pendiente de despliegue.
func estadoTrasMerge(t *Task) string {
	if len(appsDe(t)) > 0 {
		return DesplieguePendiente
	}
	return Integrada
}

// mainDeploy es el main de GitHub de un repo en esta vuelta.
type mainDeploy struct {
	clone, sha string
	err        error
	hecho      *DeployRepo // el deploy de esta vuelta, si ya se hizo
}

// DesplegarPendientes despliega las tareas en DESPLIEGUE_PENDIENTE (el main de cada repo, una vez por
// vuelta: cubre a todas) y devuelve un renglón por tarea para el log de la línea.
func (c Config) DesplegarPendientes(ctx context.Context) []string {
	ts, err := LoadTasks(c.Runs)
	if err != nil {
		return []string{"deploy: " + err.Error()}
	}
	mains := map[string]*mainDeploy{}
	defer borrarClones(mains)
	var log []string
	for _, t := range ts {
		if t.Est.Estado != DesplieguePendiente || !debeIntentar(t, time.Now()) {
			continue
		}
		log = append(log, c.desplegarTarea(ctx, t, mains)...)
	}
	return log
}

// debeIntentar: no reintenta un deploy fallido antes de deployReintento.
func debeIntentar(t *Task, ahora time.Time) bool {
	var previo map[string]DeployRepo
	if t.ReadJSON(deployArchivo, &previo) != nil {
		return true
	}
	for _, d := range previo {
		if !d.OK && ahora.Sub(d.Ultimo) < deployReintento {
			return false
		}
	}
	return true
}

func (c Config) desplegarTarea(ctx context.Context, t *Task, mains map[string]*mainDeploy) []string {
	var estado map[string]DeployRepo
	if t.ReadJSON(deployArchivo, &estado) != nil {
		estado = map[string]DeployRepo{}
	}
	var urls map[string]string
	_ = t.ReadJSON(PRsArchivo, &urls)
	var log []string
	todos, agotado := true, false
	for _, rc := range reposConDeploy(t) {
		repo := rc.Nombre
		if estado[repo].OK {
			continue // otra app de la tarea falló antes; esta ya salió
		}
		m, ok := mains[repo]
		if !ok {
			m = &mainDeploy{}
			m.clone, m.sha, m.err = traerMainGitHub(ctx, rc)
			mains[repo] = m
		}
		d := DeployRepo{Ultimo: time.Now(), SHA: m.sha}
		switch {
		case m.err != nil:
			d.Detalle = m.err.Error()
		case !tieneElMerge(ctx, m.clone, m.sha, urls[repo]):
			d.Detalle = "el main de GitHub (" + corto(m.sha) + ") todavía no tiene el merge de esta tarea"
		case m.hecho != nil:
			d = *m.hecho
		default:
			d = desplegarApp(ctx, rc, m.clone, m.sha)
			m.hecho = &d
		}
		d.Intentos = estado[repo].Intentos + 1
		estado[repo] = d
		t.Log("08-deploy", map[bool]string{true: "ok", false: "falla"}[d.OK], repo+": "+d.Detalle, 0)
		log = append(log, fmt.Sprintf("deploy %s (%s): %s", t.Est.ID, repo, d.Detalle))
		todos = todos && d.OK
		agotado = agotado || (!d.OK && d.Intentos >= deployMaxIntentos)
	}
	if err := t.WriteJSON(deployArchivo, estado); err != nil {
		log = append(log, "deploy "+t.Est.ID+": no se pudo guardar "+deployArchivo+": "+err.Error())
	}
	switch {
	case todos:
		if t.Transicion(DesplieguePendiente, Integrada, "integrada y desplegada") == nil {
			marcarNotificacionPendiente(t)
		}
	case agotado:
		_ = t.Transicion(DesplieguePendiente, Trabada, fmt.Sprintf("integrada, pero el despliegue falló %d veces: `fabrica desplegar %s` lo reintenta (ver %s)",
			deployMaxIntentos, t.Est.ID, deployArchivo))
	default:
		_ = SetEstado(c.Runs, t.Est.ID, DesplieguePendiente, DesplieguePendiente, "integrada; el despliegue falló, se reintenta solo: ver "+deployArchivo)
	}
	return log
}

// traerMainGitHub clona el main de GitHub del repo en una carpeta temporal nueva (historia completa,
// archivos a pedido), sin hooks. No usa el clon de la fábrica: ahí escriben los agentes, y git correría
// sus hooks y su config afuera del sandbox. Si GitHub no contesta, error: no cae al checkout local, que
// puede estar atrasado. Quien llama borra la carpeta.
func traerMainGitHub(ctx context.Context, rc RepoCliente) (string, string, error) {
	gh := rc.Ruta
	if !esRemotoGitHub(gh) {
		var err error
		if gh, err = git(ctx, rc.Ruta, "remote", "get-url", "origin"); err != nil || !esRemotoGitHub(gh) {
			return "", "", fmt.Errorf("el repo %s no tiene su origin en GitHub", rc.Nombre)
		}
	}
	url, err := urlGitHub(gh)
	if err != nil {
		return "", "", err
	}
	dir, err := os.MkdirTemp(dirTmpFabrica(), "deploy-src-"+rc.Nombre+"-")
	if err != nil {
		return "", "", err
	}
	fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	// Con el token de la fábrica (comandoGitGitHub), por https.
	cmd, err := comandoGitGitHub(fctx, "", "clone", "--quiet", "--filter=blob:none", "--single-branch", "--branch", "main", url, dir)
	if err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	enGrupo(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("no se pudo traer el main de GitHub: %s", tail(string(out)))
	}
	sha, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	return dir, sha, nil
}

// borrarClones borra los clones temporales de la vuelta.
func borrarClones(mains map[string]*mainDeploy) {
	for _, m := range mains {
		if m.clone != "" {
			os.RemoveAll(m.clone)
		}
	}
}

// tieneElMerge: el commit con que GitHub mergeó la PR de la tarea ya está en el main a desplegar.
// Sirve para squash y para merge común. Sin PR registrada, no se despliega a ciegas.
func tieneElMerge(ctx context.Context, clone, sha, url string) bool {
	if url == "" || sha == "" {
		return false
	}
	out, err := leerGH(ctx, "pr", "view", url, "--json", "mergeCommit", "-q", ".mergeCommit.oid")
	merge := strings.TrimSpace(string(out))
	if err != nil || merge == "" {
		return false
	}
	_, err = git(ctx, clone, "merge-base", "--is-ancestor", merge, sha)
	return err == nil
}

func corto(sha string) string { return sha[:min(8, len(sha))] }

// comandoDeploy: el comando de fabrica.yaml, corrido en la carpeta exportada con FABRICA_SHA.
func comandoDeploy(rc RepoCliente) []string { return []string{"bash", "-c", rc.Deploy} }

// desplegarApp exporta `sha` del clon y corre el deploy del repo; después, el smoke si lo hay.
func desplegarApp(ctx context.Context, rc RepoCliente, clone, sha string) DeployRepo {
	d := DeployRepo{Ultimo: time.Now(), SHA: sha}
	dir, err := os.MkdirTemp(dirTmpFabrica(), "deploy-"+rc.Nombre+"-")
	if err != nil {
		d.Detalle = err.Error()
		return d
	}
	defer os.RemoveAll(dir)
	tar := dir + ".tar"
	defer os.Remove(tar)
	if _, err := git(ctx, clone, "archive", "-o", tar, sha); err != nil {
		d.Detalle = "no se pudo exportar main: " + err.Error()
		return d
	}
	if out, err := exec.CommandContext(ctx, "tar", "-xf", tar, "-C", dir).CombinedOutput(); err != nil {
		d.Detalle = "no se pudo desarmar el export: " + tail(string(out))
		return d
	}
	dctx, cancel := context.WithTimeout(ctx, deployTimeout)
	defer cancel()
	args := comandoDeploy(rc)
	cmd := exec.CommandContext(dctx, args[0], args[1:]...)
	enGrupo(cmd)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FABRICA_SHA="+sha, "FABRICA_REPO="+rc.Nombre)
	if out, err := cmd.CombinedOutput(); err != nil {
		d.Detalle = "el deploy falló: " + tail(string(out))
		return d
	}
	if rc.Smoke != "" {
		if err := responde(ctx, rc.Smoke); err != nil {
			d.Detalle = "deploy ok (" + corto(sha) + ") pero " + rc.Smoke + " no responde: " + err.Error()
			return d
		}
	}
	d.OK, d.Detalle = true, "desplegado "+corto(sha)
	return d
}

// responde: el host contesta algo que no es un error del servidor.
func responde(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := cl.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 500 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}

// Desplegar es `fabrica desplegar <id> [--seco]`: despliega a pedido una tarea integrada, pendiente de
// despliegue o trabada por el despliegue (reinicia los intentos). En seco solo dice qué correría.
func (c Config) Desplegar(ctx context.Context, id string, seco bool) ([]string, error) {
	t, err := findTask(c.Runs, id)
	if err != nil {
		return nil, err
	}
	var previo map[string]DeployRepo
	trabadaPorDeploy := t.Est.Estado == Trabada && t.ReadJSON(deployArchivo, &previo) == nil
	if t.Est.Estado != DesplieguePendiente && t.Est.Estado != Integrada && !trabadaPorDeploy {
		return nil, fmt.Errorf("%s está en %s: se despliega una tarea integrada", id, t.Est.Estado)
	}
	repos := reposConDeploy(t)
	if len(repos) == 0 {
		return nil, fmt.Errorf("%s no toca ningún repo con deploy en fabrica.yaml: no hay nada que desplegar", id)
	}
	if seco {
		var lines []string
		for _, rc := range repos {
			lines = append(lines, "(seco) clona el main de GitHub de "+rc.Nombre+" en una carpeta temporal y comprueba que tiene el merge de la tarea")
			lines = append(lines, "(seco) lo exporta (git archive) a una carpeta temporal")
			lines = append(lines, "(seco) corre ahí, con FABRICA_SHA: "+rc.Deploy)
			if rc.Smoke != "" {
				lines = append(lines, "(seco) confirma que responde "+rc.Smoke)
			}
		}
		return lines, nil
	}
	if err := t.WriteJSON(deployArchivo, map[string]DeployRepo{}); err != nil { // a mano: intentos de cero
		return nil, err
	}
	if t.Est.Estado != DesplieguePendiente {
		if err := t.Transicion(t.Est.Estado, DesplieguePendiente, "despliegue pedido a mano"); err != nil {
			return nil, err
		}
	}
	mains := map[string]*mainDeploy{}
	defer borrarClones(mains)
	return c.desplegarTarea(ctx, t, mains), nil
}
