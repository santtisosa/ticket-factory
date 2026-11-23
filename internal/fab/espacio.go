package fab

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// ArchivoEspacio es la configuración de un espacio de trabajo: proyectos, fuentes y políticas.
const ArchivoEspacio = "fabrica.yaml"

// Espacio es fabrica.yaml. Los secretos nunca van acá: se leen del entorno.
type Espacio struct {
	Dir          string              `yaml:"-"` // la carpeta de fabrica.yaml
	Proyectos    map[string]Proyecto `yaml:"proyectos"`
	Fuentes      Fuentes             `yaml:"fuentes"`
	Notificacion Notificacion        `yaml:"notificacion"`
	Conocimiento *Conocimiento       `yaml:"conocimiento"`
	Reglas       string              `yaml:"reglas"` // archivo que se suma a los prompts
	Diseno       string              `yaml:"diseno"` // guía visual para la revisión de UI
	Merge        Merge               `yaml:"merge"`
	Limites      Limites             `yaml:"limites"`
}

// Proyecto agrupa los repos que una tarea puede tocar juntos.
type Proyecto struct {
	Repos        []RepoConf             `yaml:"repos"`
	Servicios    []Servicio             `yaml:"servicios"`
	Preview      *RecetaPreview         `yaml:"preview"`
	Notificacion NotificacionDeProyecto `yaml:"notificacion"`
}

// RepoConf es un repo de un proyecto.
type RepoConf struct {
	Nombre   string `yaml:"nombre"`
	Ruta     string `yaml:"ruta"`     // URL de git o carpeta local
	Check    string `yaml:"check"`    // verificación; vacío = se infiere
	Frontend bool   `yaml:"frontend"` // habilita la revisión de UX y UI
	CIManual bool   `yaml:"ci_manual"`
	Deploy   string `yaml:"deploy"` // comando posterior al merge
	Smoke    string `yaml:"smoke"`  // URL que tiene que responder después del deploy
}

// Servicio es una dependencia de las pruebas que la fábrica levanta con Docker.
type Servicio struct {
	Imagen  string            `yaml:"imagen"`
	Puerto  int               `yaml:"puerto"`  // el del contenedor; el de la máquina se elige libre
	Exporta map[string]string `yaml:"exporta"` // variables para el Builder y el chequeo; {puerto} = el de la máquina
	Entorno map[string]string `yaml:"entorno"` // variables del contenedor (docker run -e); solo valores de prueba
}

// RecetaPreview es cómo levantar la aplicación de una rama. {puerto} se reemplaza por uno libre.
type RecetaPreview struct {
	Comandos []string `yaml:"comandos"`
}

type NotificacionDeProyecto struct {
	Para string `yaml:"para"`
}

type Fuentes struct {
	GitHubIssues   *GitHubIssues   `yaml:"github-issues"`
	GitHubProjects *GitHubProjects `yaml:"github-projects"`
	BetterStack    *struct{}       `yaml:"betterstack"`
	Exec           []FuenteExec    `yaml:"exec"`
}

// GitHubIssues: los issues abiertos con la etiqueta. Sin autores, todo entra como PROPUESTA.
type GitHubIssues struct {
	Repo     string   `yaml:"repo"`
	Etiqueta string   `yaml:"etiqueta"`
	Autores  []string `yaml:"autores"`
	Proyecto string   `yaml:"proyecto"` // vacío: el único proyecto, o lo deduce la spec
}

// GitHubProjects: las tarjetas de un tablero cuyo título empieza con el prefijo.
type GitHubProjects struct {
	Owner   string `yaml:"owner"`
	Numero  int    `yaml:"numero"`
	Columna string `yaml:"columna"` // por defecto "Todo"
	Prefijo string `yaml:"prefijo"` // por defecto "fabrica:"
	// Autores: los issues y PRs de estos logins entran listos; los borradores del tablero, siempre. El
	// resto, PROPUESTA.
	Autores []string `yaml:"autores"`
}

// FuenteExec: un comando que imprime tickets en JSON (docs/fuentes.md).
type FuenteExec struct {
	Nombre  string `yaml:"nombre"`
	Comando string `yaml:"comando"`
}

type Notificacion struct {
	Canal     string `yaml:"canal"` // smtp | comentario | exec; vacío = sin notificación
	Remitente string `yaml:"remitente"`
	Comando   string `yaml:"comando"` // para canal exec
}

type Conocimiento struct {
	Repo        string   `yaml:"repo"`
	Escribibles []string `yaml:"escribibles"`
}

type Merge struct {
	Automatico     bool     `yaml:"automatico"`
	CheckRequerido string   `yaml:"check_requerido"`
	Sensibles      []string `yaml:"sensibles"`
}

type Limites struct {
	PorLlamadaUSD float64 `yaml:"por_llamada_usd"`
	PorDiaUSD     float64 `yaml:"por_dia_usd"`
}

var reNombre = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ErrSinEspacio: la carpeta no tiene fabrica.yaml.
var ErrSinEspacio = errors.New("no hay " + ArchivoEspacio + " en el espacio de trabajo (se crea con `fabrica init`)")

// CargarEspacio lee y valida dir/fabrica.yaml. Un campo desconocido es un error: un typo no se
// ignora en silencio.
func CargarEspacio(dir string) (Espacio, error) {
	b, err := os.ReadFile(filepath.Join(dir, ArchivoEspacio))
	if errors.Is(err, os.ErrNotExist) {
		return Espacio{}, ErrSinEspacio
	}
	if err != nil {
		return Espacio{}, err
	}
	e, err := LeerEspacio(b)
	if err != nil {
		return Espacio{}, fmt.Errorf("%s: %w", filepath.Join(dir, ArchivoEspacio), err)
	}
	e.Dir = dir
	e.Reglas = e.ruta(e.Reglas)
	e.Diseno = e.ruta(e.Diseno)
	if e.Conocimiento != nil {
		e.Conocimiento.Repo = e.ruta(e.Conocimiento.Repo)
	}
	return e, nil
}

// LeerEspacio decodifica y valida un fabrica.yaml.
func LeerEspacio(b []byte) (Espacio, error) {
	var e Espacio
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&e); err != nil {
		return Espacio{}, err
	}
	return e, e.validar()
}

func (e Espacio) validar() error {
	var errs []string
	mal := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if len(e.Proyectos) == 0 {
		mal("proyectos: hace falta al menos uno")
	}
	vistos := map[string]string{}
	for _, p := range sortedKeys(e.Proyectos) {
		pr := e.Proyectos[p]
		if !reNombre.MatchString(p) {
			mal("proyectos.%s: el nombre va en minúsculas, sin espacios", p)
		}
		if len(pr.Repos) == 0 {
			mal("proyectos.%s.repos: hace falta al menos uno", p)
		}
		for i, r := range pr.Repos {
			e.validarRepo(fmt.Sprintf("proyectos.%s.repos[%d]", p, i), p, r, vistos, mal)
		}
		for i, s := range pr.Servicios {
			if s.Imagen == "" || s.Puerto <= 0 {
				mal("proyectos.%s.servicios[%d]: imagen y puerto son obligatorios", p, i)
			}
			// La imagen va como último argumento de `docker run`: con "-" adelante sería una opción.
			if strings.HasPrefix(s.Imagen, "-") || strings.ContainsFunc(s.Imagen, unicode.IsSpace) {
				mal("proyectos.%s.servicios[%d].imagen: %q no es una imagen válida (sin espacios ni \"-\" al principio)", p, i, s.Imagen)
			}
		}
		if pr.Preview != nil && len(pr.Preview.Comandos) == 0 {
			mal("proyectos.%s.preview.comandos: vacío", p)
		}
	}
	e.validarFuentes(mal)
	switch n := e.Notificacion; n.Canal {
	case "", "comentario":
	case "smtp":
		if n.Remitente == "" {
			mal("notificacion.remitente: obligatorio con canal smtp")
		}
	case "exec":
		if n.Comando == "" {
			mal("notificacion.comando: obligatorio con canal exec")
		}
	default:
		mal("notificacion.canal: %q no existe (smtp, comentario o exec)", n.Canal)
	}
	if c := e.Conocimiento; c != nil && c.Repo == "" {
		mal("conocimiento.repo: obligatorio")
	}
	if e.Merge.Automatico && e.Merge.CheckRequerido == "" {
		mal("merge.check_requerido: obligatorio con merge.automatico")
	}
	for _, p := range e.Merge.Sensibles {
		if !globValido(p) {
			mal("merge.sensibles: %q no es un patrón válido", p)
		}
	}
	if e.Limites.PorLlamadaUSD < 0 || e.Limites.PorDiaUSD < 0 {
		mal("limites: no pueden ser negativos")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (e Espacio) validarRepo(campo, proyecto string, r RepoConf, vistos map[string]string, mal func(string, ...any)) {
	switch {
	case !reNombre.MatchString(r.Nombre):
		mal("%s.nombre: obligatorio, en minúsculas y sin espacios", campo)
	case vistos[r.Nombre] != "":
		mal("%s.nombre: %q ya está en el proyecto %s", campo, r.Nombre, vistos[r.Nombre])
	default:
		vistos[r.Nombre] = proyecto
	}
	if strings.TrimSpace(r.Ruta) == "" {
		mal("%s.ruta: obligatoria (URL de git o carpeta)", campo)
	}
	if r.Smoke != "" && r.Deploy == "" {
		mal("%s.smoke: sin deploy no hay nada que verificar", campo)
	}
}

func (e Espacio) validarFuentes(mal func(string, ...any)) {
	if gi := e.Fuentes.GitHubIssues; gi != nil {
		if gi.Repo == "" || gi.Etiqueta == "" {
			mal("fuentes.github-issues: repo y etiqueta son obligatorios")
		}
		if _, ok := e.Proyectos[gi.Proyecto]; gi.Proyecto != "" && !ok {
			mal("fuentes.github-issues.proyecto: %q no existe", gi.Proyecto)
		}
	}
	if gp := e.Fuentes.GitHubProjects; gp != nil && (gp.Owner == "" || gp.Numero <= 0) {
		mal("fuentes.github-projects: owner y numero son obligatorios")
	}
	for i, x := range e.Fuentes.Exec {
		if x.Nombre == "" || x.Comando == "" {
			mal("fuentes.exec[%d]: nombre y comando son obligatorios", i)
		}
	}
}

// ruta resuelve ~ y las rutas relativas a la carpeta de fabrica.yaml.
func (e Espacio) ruta(p string) string {
	switch {
	case p == "":
		return ""
	case p == "~" || strings.HasPrefix(p, "~/"):
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~"))
		}
		return p
	case filepath.IsAbs(p):
		return p
	default:
		return filepath.Join(e.Dir, p)
	}
}

// rutaRepo: una URL de git queda igual; una carpeta se resuelve como las demás rutas.
func (e Espacio) rutaRepo(p string) string {
	if strings.Contains(p, "://") || strings.HasPrefix(p, "git@") {
		return p
	}
	return e.ruta(p)
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// seccionDelEspacio: el contenido de un archivo que declara fabrica.yaml (reglas, diseno) como sección
// de un prompt, o "" si no está declarado. Lo escribe el operador: va fuera de las marcas de dato no
// confiable.
func (c Config) seccionDelEspacio(titulo string, campo func(Espacio) string) string {
	e, err := CargarEspacio(c.Home)
	if err != nil {
		return ""
	}
	p := campo(e)
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: no se pudo leer %s: %v\n", titulo, p, err)
		return ""
	}
	return "\n## " + titulo + "\n" + strings.TrimSpace(string(b)) + "\n"
}

// reglasMD: las convenciones del equipo (reglas de fabrica.yaml) para el Spec writer, el Builder y los revisores.
func (c Config) reglasMD() string {
	return c.seccionDelEspacio("Reglas del equipo", func(e Espacio) string { return e.Reglas })
}

// disenoMD: la guía visual del equipo (diseno de fabrica.yaml) para los revisores de UX y UI.
func (c Config) disenoMD() string {
	return c.seccionDelEspacio("Guía visual del equipo", func(e Espacio) string { return e.Diseno })
}
