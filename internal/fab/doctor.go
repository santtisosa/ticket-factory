package fab

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Init escribe dir/fabrica.yaml con el ejemplo si todavía no existe.
func Init(dir string, ejemplo []byte) (string, error) {
	p := filepath.Join(dir, ArchivoEspacio)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return p, fmt.Errorf("%s ya existe: no se sobrescribe", p)
	}
	if err != nil {
		return p, err
	}
	if _, err := f.Write(ejemplo); err != nil {
		f.Close()
		return p, err
	}
	return p, f.Close()
}

// Chequeo es una línea del informe de `fabrica doctor`.
type Chequeo struct {
	Nombre    string
	OK        bool
	Requerido bool
	Detalle   string
}

// Doctor revisa lo que la fábrica necesita en dir. buscar es exec.LookPath (variable para los tests) y
// token dice si FABRICA_GITHUB_TOKEN está definido (en el entorno o en el archivo env). Devuelve los
// chequeos y si pasaron todos los requeridos.
func Doctor(dir string, buscar func(string) (string, error), token bool) ([]Chequeo, bool) {
	var cs []Chequeo
	bin := func(nombre string, requerido bool, para string) {
		ch := Chequeo{Nombre: nombre, Requerido: requerido}
		if p, err := buscar(nombre); err == nil {
			ch.OK, ch.Detalle = true, p
		} else {
			ch.Detalle = "no está en el PATH (" + para + ")"
		}
		cs = append(cs, ch)
	}
	e, errEsp := CargarEspacio(dir)
	conServicios := false
	for _, p := range e.Proyectos {
		conServicios = conServicios || len(p.Servicios) > 0
	}
	bin("git", true, "worktrees y commits")
	bin("claude", true, "Claude Code ejecuta cada etapa")
	bin("srt", true, "aislamiento del código de la rama: npm install -g @anthropic-ai/sandbox-runtime")
	bin("docker", conServicios, "servicios de prueba de fabrica.yaml")
	bin("gh", false, "pull requests y CI")
	tk := Chequeo{Nombre: VarTokenGitHub, OK: token, Requerido: false, Detalle: "definido"}
	if !token {
		tk.Detalle = "no definido: sin él no se publica la rama ni se siguen las PRs"
	}
	cs = append(cs, tk)
	esp := Chequeo{Nombre: ArchivoEspacio, OK: errEsp == nil, Requerido: true, Detalle: filepath.Join(dir, ArchivoEspacio)}
	if errEsp != nil {
		esp.Detalle = errEsp.Error()
	}
	cs = append(cs, esp)
	// Los archivos que declara fabrica.yaml: un aviso, no un error (los prompts siguen sin ellos).
	for _, a := range [][2]string{{"reglas", e.Reglas}, {"diseno", e.Diseno}} {
		if a[1] != "" {
			cs = append(cs, existe(a[0], a[1]))
		}
	}
	if e.Conocimiento != nil && !esRemotoGitHub(e.Conocimiento.Repo) {
		cs = append(cs, existe("conocimiento.repo", e.Conocimiento.Repo))
	}
	ok := true
	for _, c := range cs {
		ok = ok && (c.OK || !c.Requerido)
	}
	return cs, ok
}

func existe(nombre, p string) Chequeo {
	ch := Chequeo{Nombre: nombre, Detalle: p}
	if _, err := os.Stat(p); err == nil {
		ch.OK = true
	} else {
		ch.Detalle = p + ": no existe"
	}
	return ch
}

// BuscarBinario es exec.LookPath, para Doctor.
var BuscarBinario = exec.LookPath

// TokenGitHubDefinido: FABRICA_GITHUB_TOKEN en el entorno o en el archivo env (lo carga loadEnvFile).
func TokenGitHubDefinido() (bool, error) {
	if err := loadEnvFile(); err != nil {
		return false, err
	}
	return os.Getenv(VarTokenGitHub) != "" || tokenGitHub != "", nil
}
