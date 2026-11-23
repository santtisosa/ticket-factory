package fab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func homeDePrueba(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	escribirEspacio(t, home, `proyectos:
  demo:
    repos:
      - {nombre: demo-backend, ruta: `+filepath.Join(home, "repos", "demo-backend")+`}
      - {nombre: demo-frontend, ruta: `+filepath.Join(home, "repos", "demo-frontend")+`, frontend: true}
  ejemplo:
    repos:
      - {nombre: ejemplo-backend, ruta: `+filepath.Join(home, "repos", "ejemplo-backend")+`}
  tienda:
    repos:
      - {nombre: tienda, ruta: `+filepath.Join(home, "tienda")+`, frontend: true}
  acme:
    repos:
      - {nombre: acme, ruta: https://github.com/x/acme.git, frontend: true, deploy: ./desplegar.sh, smoke: "https://acme.example.com/"}
`)
	return home
}

// escribirEspacio deja un fabrica.yaml en home.
func escribirEspacio(t *testing.T, home, yaml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, ArchivoEspacio), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// La tabla sale de los proyectos de fabrica.yaml, en orden y con su ruta resuelta.
func TestClientes(t *testing.T) {
	cs := Clientes(homeDePrueba(t))
	var slugs []string
	for _, c := range cs {
		slugs = append(slugs, c.Slug)
	}
	if len(cs) != 4 || slugs[0] != "acme" || slugs[1] != "demo" || slugs[3] != "tienda" {
		t.Fatalf("clientes: %v", slugs)
	}
	if r, slug, ok := buscarRepo(cs, "demo-frontend"); !ok || slug != "demo" || !r.Front {
		t.Fatalf("demo-frontend: %+v %s %v", r, slug, ok)
	}
	if _, _, ok := buscarRepo(cs, "no-declarado"); ok {
		t.Fatal("un repo que no está en fabrica.yaml no es de ningún proyecto")
	}
	if r, _, _ := buscarRepo(cs, "acme"); r.Ruta != "https://github.com/x/acme.git" || r.Deploy == "" {
		t.Fatalf("una URL de git queda igual y el deploy viaja: %+v", r)
	}
	if ClienteDeServicio(cs, "ejemplo-backend") != "ejemplo" || ClienteDeServicio(cs, "otro") != "" {
		t.Fatal("servicio → cliente")
	}
}

func TestValidateSpec(t *testing.T) {
	cs := Clientes(homeDePrueba(t))
	ok := specWith("CA-1", "CA-2")
	ok.Repos = []string{"demo-backend", "demo-frontend"}
	if err := validateSpec(ok, "demo", cs); err != nil {
		t.Fatalf("válida: %v", err)
	}
	casos := map[string]func(s *Spec) string{
		"repo desconocido":    func(s *Spec) string { s.Repos = []string{"x-backend"}; return "" },
		"mezcla clientes":     func(s *Spec) string { s.Repos = []string{"demo-backend", "ejemplo-backend"}; return "" },
		"otro cliente":        func(s *Spec) string { s.Repos = []string{"ejemplo-backend"}; return "demo" },
		"criterios con hueco": func(s *Spec) string { s.Criterios[1].ID = "CA-3"; return "" },
		"frena sin motivo":    func(s *Spec) string { s.Frenar = true; return "" },
	}
	for nombre, romper := range casos {
		s := specWith("CA-1", "CA-2")
		s.Repos = []string{"demo-backend"}
		cliente := romper(&s)
		if err := validateSpec(s, cliente, cs); err == nil {
			t.Errorf("%s tenía que fallar", nombre)
		}
	}
}

// El enum de repos del esquema es el de la entrada: el modelo no puede elegir otro repo.
func TestSpecSchemaArmaElEnum(t *testing.T) {
	var sch map[string]any
	if err := json.Unmarshal(specSchema([]string{"ejemplo-backend"}), &sch); err != nil {
		t.Fatal(err)
	}
	enum := sch["properties"].(map[string]any)["repos"].(map[string]any)["items"].(map[string]any)["enum"].([]any)
	if len(enum) != 1 || enum[0] != "ejemplo-backend" {
		t.Fatalf("enum: %v", enum)
	}
}

func TestCheckCmdSeDescubre(t *testing.T) {
	dir := t.TempDir()
	if CheckCmd(Repo{WT: dir}) == "make check" {
		t.Fatal("sin Makefile no puede ser make check")
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("lint:\n\techo\n\ncheck: lint\n\techo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if CheckCmd(Repo{WT: dir}) != "make check" {
		t.Fatal("con target check, el chequeo es make check")
	}
	if CheckCmd(Repo{WT: dir, Check: "go test ./..."}) != "go test ./..." {
		t.Fatal("el check de fabrica.yaml manda sobre lo que se infiere")
	}

	// Una app suelta con `npm run check` usa ese, que es el de su CI; sin él, lint y test.
	app := t.TempDir()
	if err := os.WriteFile(filepath.Join(app, "package.json"), []byte(`{"scripts":{"test":"node --test"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(CheckCmd(Repo{WT: app}), "npm run check") {
		t.Fatal("sin script check no se inventa")
	}
	if err := os.WriteFile(filepath.Join(app, "package.json"), []byte(`{"scripts":{"check":"perl tools/check-tdz.pl *.html && npm test","test":"node --test"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(CheckCmd(Repo{WT: app}), "npm run check") {
		t.Fatalf("con script check, el chequeo es npm run check: %q", CheckCmd(Repo{WT: app}))
	}
}

// Un log trae su servicio: la tarea nace sabiendo de qué proyecto es.
func TestIngestAnotaElCliente(t *testing.T) {
	home := homeDePrueba(t)
	runs := filepath.Join(home, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	src := fakeSource{[]Item{{Clave: "log:1", Fuente: "log", Texto: "panic", Servicio: "ejemplo-backend"}}}
	Ingest(context.Background(), runs, []Source{src})
	ts, err := LoadTasks(runs)
	if err != nil || len(ts) != 1 {
		t.Fatalf("tareas: %v %v", ts, err)
	}
	var e Entrada
	if err := ts[0].ReadJSON("00-entrada.json", &e); err != nil || e.Cliente != "ejemplo" {
		t.Fatalf("cliente: %q %v", e.Cliente, err)
	}
}

// Una spec que frenó se retoma con las respuestas, sin volver a llamar al Spec writer.
func TestContinuarRetomaLaSpec(t *testing.T) {
	home := homeDePrueba(t)
	runs := filepath.Join(home, "repos", "fabrica", "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	tk, err := AddTask(runs, "x", "prompt", Pendiente, "demo")
	if err != nil {
		t.Fatal(err)
	}
	s := specWith("CA-1")
	s.Repos, s.Frenar, s.MotivoFrenar = []string{"demo-backend"}, true, "¿en pesos o dólares?"
	if err := tk.WriteJSON("01-spec.json", s); err != nil {
		t.Fatal(err)
	}
	if err := tk.Transicion(Pendiente, Trabada, "spec frenó"); err != nil {
		t.Fatal(err)
	}
	if err := PedirContinuar(runs, tk.Est.ID, "en dólares"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Config{}.continuar(tk, "demo", Clientes(home))
	if !ok || err != nil || got.Frenar || got.Decisiones != "en dólares" {
		t.Fatalf("continuar: %+v %v %v", got, ok, err)
	}
	if _, ok, _ := (Config{}).continuar(tk, "demo", Clientes(home)); ok {
		t.Fatal("las respuestas se usan una sola vez")
	}
}

// Sin CI la PR no tiene checks: no pasa a CI verde, pero sí a mergeada.
func TestEstadoSinChecks(t *testing.T) {
	if e := estadoDePRs([]estadoPR{{State: "OPEN"}}); e != "" {
		t.Fatalf("abierta sin checks: %q, quería que no cambie", e)
	}
	if e := estadoDePRs([]estadoPR{{State: "MERGED"}}); e != Integrada {
		t.Fatalf("mergeada: %q", e)
	}
}
