package fab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// La línea toma primero la de más prioridad; empate, la más vieja.
// Una tarea por cliente: con acme ocupado va la de ejemplo, aunque la de acme sea más vieja.
func TestSiguienteLibreUnaPorCliente(t *testing.T) {
	runs := t.TempDir()
	c1, _ := AddTask(runs, "c1", "ticket", Pendiente, "acme")
	_, _ = AddTask(runs, "c2", "ticket", Pendiente, "acme")
	p1, _ := AddTask(runs, "p1", "ticket", Pendiente, "ejemplo")
	s1, _ := AddTask(runs, "s1", "prompt", Pendiente, "")
	if s, c, _ := SiguienteLibre(runs, map[string]bool{}); s == nil || s.Est.ID != c1.Est.ID || c != "acme" {
		t.Fatalf("libre todo: va la más vieja, c1: %+v %q", s, c)
	}
	if s, c, _ := SiguienteLibre(runs, map[string]bool{"acme": true}); s == nil || s.Est.ID != p1.Est.ID || c != "ejemplo" {
		t.Fatalf("con acme ocupado va p1: %+v %q", s, c)
	}
	if s, c, _ := SiguienteLibre(runs, map[string]bool{"acme": true, "ejemplo": true}); s == nil || s.Est.ID != s1.Est.ID || c != "sin-cliente" {
		t.Fatalf("sin cliente va por su carril: %+v %q", s, c)
	}
	if s, _, _ := SiguienteLibre(runs, map[string]bool{"acme": true, "ejemplo": true, "sin-cliente": true}); s != nil {
		t.Fatalf("todo ocupado: nada, y tomó %+v", s)
	}
}

func TestSiguientePendientePorPrioridad(t *testing.T) {
	runs := t.TempDir()
	a, _ := AddTask(runs, "a", "prompt", Pendiente, "")
	b, _ := AddTask(runs, "b", "prompt", Pendiente, "")
	if s, _ := SiguientePendiente(runs); s == nil || s.Est.ID != a.Est.ID {
		t.Fatalf("sin prioridad va la más vieja: %+v", s)
	}
	if err := SetPrioridad(runs, b.Est.ID, 5); err != nil {
		t.Fatal(err)
	}
	if s, _ := SiguientePendiente(runs); s == nil || s.Est.ID != b.Est.ID {
		t.Fatalf("con prioridad va b: %+v", s)
	}
	if err := SetPrioridad(runs, "no-existe", 1); err == nil {
		t.Fatal("una tarea que no existe es error")
	}
}

func TestCancelarYReintentar(t *testing.T) {
	runs := t.TempDir()
	a, _ := AddTask(runs, "a", "prompt", EnCurso, "")
	if err := Cancelar(runs, a.Est.ID); err == nil || !strings.Contains(err.Error(), "corriendo") {
		t.Fatalf("una EN_CURSO no se cancela: %v", err)
	}
	b, _ := AddTask(runs, "b", "prompt", Trabada, "")
	if err := Reintentar(runs, b.Est.ID); err != nil {
		t.Fatal(err)
	}
	if err := Cancelar(runs, b.Est.ID); err != nil {
		t.Fatal(err)
	}
	if err := Reintentar(runs, b.Est.ID); err == nil {
		t.Fatal("una descartada no se reintenta")
	}
}

func TestPausa(t *testing.T) {
	runs := t.TempDir()
	if Pausada(runs) {
		t.Fatal("arranca sin pausa")
	}
	if err := Pausar(runs); err != nil || !Pausada(runs) {
		t.Fatal(err)
	}
	if err := Seguir(runs); err != nil || Pausada(runs) {
		t.Fatal(err)
	}
	if err := Seguir(runs); err != nil {
		t.Fatal("seguir dos veces no es error")
	}
}

func TestVerMuestraClienteYEventos(t *testing.T) {
	runs := t.TempDir()
	a, _ := AddTask(runs, "arreglar el saldo", "log", Pendiente, "ejemplo")
	a.Log("02-spec", "inicio", "spec-writer", 0)
	s, err := Ver(runs, a.Est.ID)
	if err != nil || !strings.Contains(s, "proyecto ejemplo") || !strings.Contains(s, "spec-writer") || !strings.Contains(s, "00-entrada.json") {
		t.Fatalf("%v\n%s", err, s)
	}
}

func TestEstadoDePRs(t *testing.T) {
	ok := func(c string) estadoPR {
		p := estadoPR{State: "OPEN"}
		p.Checks = append(p.Checks, checkPR{Conclusion: c, Status: "COMPLETED"})
		return p
	}
	casos := []struct {
		nombre string
		prs    []estadoPR
		quiere string
	}{
		{"sin checks todavía", []estadoPR{{State: "OPEN"}}, ""},
		{"verde", []estadoPR{ok("SUCCESS")}, CIVerde},
		{"un rojo manda", []estadoPR{ok("SUCCESS"), ok("FAILURE")}, CIRojo},
		{"mergeada", []estadoPR{{State: "MERGED"}}, Integrada},
		{"una mergeada y otra verde", []estadoPR{{State: "MERGED"}, ok("SUCCESS")}, CIVerde},
		{"no se pudo leer", nil, ""},
		{"cerrada sin merge", []estadoPR{{State: "CLOSED"}}, Descartada},
		{"pide acción", []estadoPR{ok("ACTION_REQUIRED")}, CIRojo},
	}
	for _, c := range casos {
		if got := estadoDePRs(c.prs); got != c.quiere {
			t.Errorf("%s: %q, quería %q", c.nombre, got, c.quiere)
		}
	}
}

// El cuerpo de la PR trae lo que hace falta para revisarla sin abrir la fábrica.
func TestCuerpoPR(t *testing.T) {
	runs := t.TempDir()
	tk, _ := AddTask(runs, "x", "prompt", EsperandoAprobacion, "")
	tk.Est.Vuelta = 1
	_ = tk.WriteJSON("02-build-v1.json", Build{Resumen: "agregué el filtro", TestsAceptacion: []TestAceptacion{{CA: "CA-1", Repo: "r", Archivo: "a_acceptance_test.go", Test: "TestCA1"}}})
	_ = tk.WriteJSON("04-approver-v1.json", Decision{Pasa: true, Descartado: []Hallazgo{{Regla: "fuera-de-alcance", Severidad: "LOW"}}, SinRevisar: []string{"ux: sin app"}})
	s := Spec{Titulo: "Filtro", Tipo: "bug", SpecMD: "# Filtro"}
	c := cuerpoPR(tk, s)
	for _, quiere := range []string{"# Filtro", "CA-1: `TestCA1`", "agregué el filtro", "Fuera de alcance", "ux: sin app", tk.Est.ID} {
		if !strings.Contains(c, quiere) {
			t.Errorf("falta %q en:\n%s", quiere, c)
		}
	}
	if tituloPR(s) != "fix: Filtro" {
		t.Error(tituloPR(s))
	}
}

// Un CI rojo se re-corre una sola vez, con el run que sale del link del check que falló.
func TestReintentarCI(t *testing.T) {
	var llamadas [][]string
	orig := correrGH
	correrGH = func(_ context.Context, args ...string) error { llamadas = append(llamadas, args); return nil }
	defer func() { correrGH = orig }()
	rojo := []estadoPR{{State: "OPEN", Checks: []checkPR{
		{Conclusion: "FAILURE", DetailsURL: "https://github.com/organizacion/demo-web/actions/runs/41262719986/job/1"},
		{Conclusion: "FAILURE", DetailsURL: "https://github.com/organizacion/demo-web/actions/runs/41262719986/job/2"},
		{Conclusion: "SUCCESS", DetailsURL: "https://github.com/organizacion/demo-web/actions/runs/999/job/3"},
	}}}
	if r := runsFallidos(rojo); len(r) != 1 || r[0] != [2]string{"organizacion/demo-web", "41262719986"} {
		t.Fatalf("runs fallidos: %v", r)
	}
	runs := t.TempDir()
	task := &Task{Dir: t.TempDir(), runs: runs, Est: Estado{ID: "x"}}
	if !reintentarCI(context.Background(), task, rojo) {
		t.Fatal("la primera vez re-corre")
	}
	if len(llamadas) != 1 || llamadas[0][2] != "41262719986" || llamadas[0][3] != "--failed" {
		t.Fatalf("gh run rerun <id> --failed: %v", llamadas)
	}
	if reintentarCI(context.Background(), task, rojo) {
		t.Fatal("la segunda vez ya no: queda en CI_ROJO")
	}
}

// L1: con un job todavía corriendo no se decide rojo (ni se relanza) hasta que termine.
func TestHayChecksCorriendo(t *testing.T) {
	corriendo := []estadoPR{{Checks: []checkPR{{Conclusion: "FAILURE", Status: "COMPLETED"}, {Status: "IN_PROGRESS"}}}}
	terminado := []estadoPR{{Checks: []checkPR{{Conclusion: "FAILURE", Status: "COMPLETED"}, {Conclusion: "SUCCESS", Status: "COMPLETED"}}}}
	if !hayChecksCorriendo(corriendo) || hayChecksCorriendo(terminado) {
		t.Fatal("corriendo sí, terminado no")
	}
	task := &Task{Dir: t.TempDir()}
	if reintentoReciente(task) {
		t.Fatal("sin reintento no hay reintento reciente")
	}
	_ = task.WriteJSON(ciReintentado, []string{"x#1"})
	if !reintentoReciente(task) {
		t.Fatal("recién relanzado: espera a la corrida nueva")
	}
}

// Nada se publica sin la confirmación de una persona, venga de la página o de la terminal: una tarea
// que no espera la aprobación ni pregunta, y un "no" corta antes del push.
func TestAprobarPideConfirmacion(t *testing.T) {
	cf := &confirmadorFalso{}
	usarConfirmador(t, cf, nil)
	runs, home := t.TempDir(), t.TempDir()
	escribirEspacio(t, home, "proyectos:\n  demo:\n    repos:\n      - {nombre: demo-api, ruta: https://github.com/x/demo-api.git}\n")
	c := Config{Runs: runs, Home: home}
	ctx := context.Background()

	otra, _ := AddTask(runs, "en curso", "prompt", EnCurso, "")
	if _, err := c.Aprobar(ctx, otra.Est.ID, false, false); err == nil || len(cf.textos) != 0 {
		t.Fatalf("tarea en curso: err=%v preguntas=%v", err, cf.textos)
	}

	tk, _ := AddTask(runs, `ticket con "comillas"`, "prompt", EsperandoAprobacion, "")
	_ = tk.WriteJSON("01-spec.json", Spec{Titulo: "x", Repos: []string{"demo-api"}})
	_ = tk.WriteJSON("aprobado.json", map[string]string{"demo-api": "abc"})
	if _, err := c.Aprobar(ctx, tk.Est.ID, false, true); err != errSinConfirmar {
		t.Fatalf("sin confirmar: %v", err)
	}
	if len(cf.textos) != 1 || !strings.HasPrefix(cf.textos[0], "Publicar en GitHub: demo-api\n\n"+`ticket con "comillas"`) ||
		!strings.Contains(cf.textos[0], "Pedido desde el panel web") || cf.claves[0] != tk.Est.ID {
		t.Fatalf("preguntas: %q claves %q", cf.textos, cf.claves)
	}
	if _, err := c.Aprobar(ctx, tk.Est.ID, true, false); err != nil && strings.Contains(err.Error(), "confirm") || len(cf.textos) != 1 {
		t.Fatalf("el seco no pregunta: %v %v", err, cf.textos)
	}

	// Un repo de la spec que no es del espacio no llega al diálogo.
	_ = tk.WriteJSON("01-spec.json", Spec{Titulo: "x", Repos: []string{"otro\nPublicar en GitHub: nada"}})
	if _, err := c.Aprobar(ctx, tk.Est.ID, false, false); err == nil || len(cf.textos) != 1 {
		t.Fatalf("repo ajeno: %v %v", err, cf.textos)
	}
	_ = tk.WriteJSON("01-spec.json", Spec{Titulo: "x", Repos: []string{"demo-api"}})

	usarConfirmador(t, nil, errSinCanal)
	if _, err := c.Aprobar(ctx, tk.Est.ID, false, false); err != errSinCanal {
		t.Fatalf("sin canal seguro no se publica: %v", err)
	}
}

// Solo el Builder (el único con Bash libre) corre en el sandbox, sin escape y sin github.com.
func TestSandboxSoloBuilder(t *testing.T) {
	dir := t.TempDir()
	c := Config{Home: dir, Factory: filepath.Join(dir, "repos", "fabrica")}
	leer := func(kind string) map[string]any {
		p := filepath.Join(dir, kind+".json")
		if err := c.writeSettings(p, kind); err != nil {
			t.Fatal(err)
		}
		var s map[string]any
		b, _ := os.ReadFile(p)
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if _, ok := leer("")["sandbox"]; ok {
		t.Error("un revisor sin Bash libre no necesita sandbox")
	}
	sb, _ := leer("builder")["sandbox"].(map[string]any)
	if sb["enabled"] != true || sb["allowUnsandboxedCommands"] != false || sb["failIfUnavailable"] != true {
		t.Fatalf("sandbox: %v", sb)
	}
	permisos, _ := json.Marshal(leer("builder")["permissions"])
	if strings.Contains(string(permisos), "fabrica ask") || !strings.Contains(string(permisos), "fabrica aprobar") {
		t.Errorf("el Builder le pregunta al Jefe pero no da la aprobación: %s", permisos)
	}
	red, _ := json.Marshal(sb["network"])
	if strings.Contains(string(red), "github") {
		t.Errorf("el Builder no sale a GitHub: %s", red)
	}
}

// CI a mano (6a3d, d429): la PR no trae checks y el estado sale de la corrida de ci.yml en la rama.
func TestChecksDeCIManual(t *testing.T) {
	viejo := salidaGH
	t.Cleanup(func() { salidaGH = viejo })
	var pedido []string
	for _, c := range []struct {
		salida string
		quiero string
	}{
		{`[{"status":"completed","conclusion":"success","url":"https://github.com/organizacion/demo-api/actions/runs/1"}]`, CIVerde},
		{`[{"status":"in_progress","conclusion":"","url":"u"}]`, ""},
		{`[{"status":"completed","conclusion":"failure","url":"u"}]`, CIRojo},
		{`[]`, ""}, // todavía no corrió: sigue esperando
	} {
		salidaGH = func(_ context.Context, args ...string) ([]byte, error) { pedido = args; return []byte(c.salida), nil }
		p := estadoPR{State: "OPEN", Checks: checksDeCIManual(context.Background(), "https://github.com/organizacion/demo-api/pull/18", "factory/x")}
		if got := estadoDePRs([]estadoPR{p}); got != c.quiero {
			t.Errorf("%s: estado %q, quería %q", c.salida, got, c.quiero)
		}
	}
	if !strings.Contains(strings.Join(pedido, " "), "--repo organizacion/demo-api --branch factory/x --workflow ci.yml") {
		t.Errorf("pidió %v", pedido)
	}
}
