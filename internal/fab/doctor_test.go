package fab

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	ticketfactory "github.com/santtisosa/ticket-factory"
)

// init escribe un fabrica.yaml válido y no pisa uno existente.
func TestInit(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir, ticketfactory.EjemploEspacio); err != nil {
		t.Fatal(err)
	}
	if _, err := CargarEspacio(dir); err != nil {
		t.Fatalf("el ejemplo no valida: %v", err)
	}
	if _, err := Init(dir, []byte("x")); err == nil {
		t.Fatal("tenía que negarse a sobrescribir")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ArchivoEspacio)); string(b) == "x" {
		t.Fatal("sobrescribió")
	}
}

func porNombre(cs []Chequeo) map[string]Chequeo {
	m := map[string]Chequeo{}
	for _, c := range cs {
		m[c.Nombre] = c
	}
	return m
}

func TestDoctor(t *testing.T) {
	todos := func(string) (string, error) { return "/usr/bin/x", nil }
	sinDocker := func(n string) (string, error) {
		if n == "docker" || n == "gh" {
			return "", errors.New("no")
		}
		return "/usr/bin/" + n, nil
	}
	sinYaml := t.TempDir()
	if cs, ok := Doctor(sinYaml, todos, true); ok || porNombre(cs)[ArchivoEspacio].OK {
		t.Fatalf("sin fabrica.yaml no pasa: %+v", cs)
	}

	// Sin servicios, docker y gh son opcionales; el token también.
	dir := t.TempDir()
	escribirEspacio(t, dir, "proyectos:\n  demo:\n    repos:\n      - {nombre: demo-api, ruta: /tmp/demo-api}\n")
	cs, ok := Doctor(dir, sinDocker, false)
	m := porNombre(cs)
	if !ok || m["docker"].OK || m["docker"].Requerido || m[VarTokenGitHub].OK {
		t.Fatalf("sin servicios docker es opcional: %+v", cs)
	}

	// Con servicios, docker es requerido.
	escribirEspacio(t, dir, "proyectos:\n  demo:\n    repos:\n      - {nombre: demo-api, ruta: /tmp/demo-api}\n"+
		"    servicios:\n      - {imagen: redis:7, puerto: 6379}\n")
	if cs, ok := Doctor(dir, sinDocker, true); ok || !porNombre(cs)["docker"].Requerido {
		t.Fatalf("con servicios docker es requerido: %+v", cs)
	}

	// Sin srt no pasa.
	sinSrt := func(n string) (string, error) {
		if n == "srt" {
			return "", errors.New("no")
		}
		return "/usr/bin/" + n, nil
	}
	if _, ok := Doctor(dir, sinSrt, true); ok {
		t.Fatal("sin srt no pasa")
	}
}

// reglas y diseno de fabrica.yaml llegan a los prompts; sin declararlos, nada.
func TestReglasYDiseno(t *testing.T) {
	dir := t.TempDir()
	base := "proyectos:\n  demo:\n    repos:\n      - {nombre: demo-api, ruta: /tmp/demo-api}\n"
	escribirEspacio(t, dir, base)
	c := Config{Home: dir}
	if c.reglasMD() != "" || c.disenoMD() != "" {
		t.Fatal("sin declarar no hay sección")
	}
	_ = os.WriteFile(filepath.Join(dir, "reglas.md"), []byte("Los importes van en centavos.\n"), 0o644)
	escribirEspacio(t, dir, base+"reglas: reglas.md\ndiseno: no-existe.md\n")
	if r := c.reglasMD(); r != "\n## Reglas del equipo\nLos importes van en centavos.\n" {
		t.Fatalf("reglas: %q", r)
	}
	if c.disenoMD() != "" {
		t.Fatal("un archivo que no existe no suma nada")
	}
}
