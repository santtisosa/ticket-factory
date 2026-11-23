package fab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// El bloque de configuración del README es el contrato público: tiene que cargar tal cual.
func TestEspacioDelREADME(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	k := strings.Index(s, "## Configuración")
	i := strings.Index(s[k:], "```yaml\n")
	if k < 0 || i < 0 {
		t.Fatal("el README no tiene el bloque yaml de Configuración")
	}
	bloque := s[k+i+len("```yaml\n"):]
	bloque = bloque[:strings.Index(bloque, "```")]
	e, err := LeerEspacio([]byte(bloque))
	if err != nil {
		t.Fatalf("el yaml del README no carga: %v", err)
	}
	if len(e.Proyectos["demo"].Repos) != 2 || e.Fuentes.GitHubIssues == nil || e.Notificacion.Canal != "smtp" {
		t.Fatalf("el yaml del README cargó incompleto: %+v", e)
	}
}

func TestEspacioDelEjemplo(t *testing.T) {
	dir := t.TempDir()
	b, err := os.ReadFile(filepath.Join("..", "..", "fabrica.yaml.ejemplo"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ArchivoEspacio), b, 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := CargarEspacio(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e.Reglas != filepath.Join(dir, "reglas-del-equipo.md") {
		t.Errorf("reglas no se resolvió contra la carpeta: %q", e.Reglas)
	}
	if got := e.rutaRepo(e.Proyectos["demo"].Repos[0].Ruta); got != "git@github.com:organizacion/demo-api.git" {
		t.Errorf("una URL de git no se toca: %q", got)
	}
}

func TestEspacioRechaza(t *testing.T) {
	base := "proyectos:\n  p:\n    repos:\n      - nombre: a\n        ruta: /x\n"
	casos := map[string]string{
		"campo desconocido":          base + "limite:\n  por_dia_usd: 1\n",
		"sin proyectos":              "limites:\n  por_dia_usd: 1\n",
		"repo repetido":              base + "  q:\n    repos:\n      - nombre: a\n        ruta: /y\n",
		"repo sin ruta":              "proyectos:\n  p:\n    repos:\n      - nombre: a\n",
		"smtp sin remitente":         base + "notificacion:\n  canal: smtp\n",
		"canal inexistente":          base + "notificacion:\n  canal: fax\n",
		"issues sin etiqueta":        base + "fuentes:\n  github-issues:\n    repo: o/r\n",
		"merge automático sin check": base + "merge:\n  automatico: true\n",
		"smoke sin deploy":           "proyectos:\n  p:\n    repos:\n      - nombre: a\n        ruta: /x\n        smoke: http://x\n",
		"imagen como opción":         base + "    servicios:\n      - {imagen: \"--privileged\", puerto: 3306}\n",
		"imagen con espacios":        base + "    servicios:\n      - {imagen: \"mysql:8 -v /:/h\", puerto: 3306}\n",
	}
	if _, err := LeerEspacio([]byte(base + "    servicios:\n      - {imagen: \"mysql:8.4\", puerto: 3306}\n")); err != nil {
		t.Errorf("una imagen normal no carga: %v", err)
	}
	for nombre, y := range casos {
		if _, err := LeerEspacio([]byte(y)); err == nil {
			t.Errorf("%s: lo aceptó", nombre)
		}
	}
	if _, err := LeerEspacio([]byte(base)); err != nil {
		t.Errorf("el mínimo válido no carga: %v", err)
	}
	if _, err := CargarEspacio(t.TempDir()); err != ErrSinEspacio {
		t.Errorf("sin fabrica.yaml: %v", err)
	}
}
