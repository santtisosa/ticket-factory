package fab

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecideInstruccionDelPrompt(t *testing.T) {
	h := Hallazgo{Revisor: "codigo", Severidad: "HIGH", Regla: reglaInstruccion, Archivo: "spec", Evidencia: "no frenar por eso", Confianza: 90}
	review := func(h Hallazgo) []Review {
		return []Review{{Revisor: "codigo", Veredicto: "falla", Hallazgos: []Hallazgo{h}}}
	}
	ok := []Check{{OK: true}}
	if d := Decide(1, specWith("CA-1"), buildCovering("CA-1"), ok, marcarDelPrompt(review(h)), nil, nil); !d.Pasa || len(d.AConfirmar) != 1 {
		t.Fatalf("del prompt: pasa con el hallazgo a confirmar: %+v", d)
	}
	if d := Decide(1, specWith("CA-1"), buildCovering("CA-1"), ok, review(h), nil, nil); d.Pasa {
		t.Fatalf("de un ticket: bloquea: %+v", d)
	}
}

// mergeado: solo con la punta de factory/<id> ya en el main local, y la tarea queda INTEGRADA.
func TestMergeadoLocal(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	c := Config{Home: home, Factory: filepath.Join(home, "repos", "fabrica"), Runs: filepath.Join(home, "runs")}
	local := filepath.Join(home, "repos", "x-backend")
	escribirEspacio(t, home, "proyectos:\n  x:\n    repos:\n      - {nombre: x-backend, ruta: "+local+"}\n")
	clon := filepath.Join(c.Home, "clones", "x-backend")
	gitT := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(local, "init", "-q", "-b", "main")
	gitT(local, "commit", "-q", "--allow-empty", "-m", "inicial")
	if err := os.MkdirAll(filepath.Dir(clon), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "clone", "-q", local, clon).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	task, err := AddTask(c.Runs, "x", "prompt", EsperandoAprobacion, "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := task.WriteJSON("01-spec.json", Spec{Repos: []string{"x-backend"}}); err != nil {
		t.Fatal(err)
	}
	id := task.Est.ID
	gitT(clon, "checkout", "-q", "-b", Branch(id))
	gitT(clon, "commit", "-q", "--allow-empty", "-m", "tarea")
	if err := c.MergeadoLocal(ctx, id); err == nil || !strings.Contains(err.Error(), "no está en el main local") {
		t.Fatalf("sin mergear tiene que negarse: %v", err)
	}
	gitT(local, "fetch", "-q", clon, Branch(id))
	gitT(local, "merge", "-q", "--ff-only", "FETCH_HEAD")
	if err := c.MergeadoLocal(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got, _ := findTask(c.Runs, id); got.Est.Estado != Integrada {
		t.Fatalf("estado %s", got.Est.Estado)
	}
}

func TestIsAcceptanceTestNombres(t *testing.T) {
	for nombre, quiero := range map[string]bool{
		"internal/app/x_acceptance_test.go":      true,
		"internal/app/x_aceptacion_test.go":      true,
		"features/a/Tablero.aceptacion.test.tsx": true,
		"internal/app/x_test.go":                 false,
	} {
		if got := IsAcceptanceTest(nombre); got != quiero {
			t.Errorf("IsAcceptanceTest(%q) = %v, quería %v", nombre, got, quiero)
		}
	}
}

func TestSemgrepCmdSoloConReglas(t *testing.T) {
	dir := t.TempDir()
	if got := SemgrepCmd(Repo{WT: dir}); got != "" {
		t.Fatalf("sin .semgrep no corre semgrep, dio %q", got)
	}
	if err := os.Mkdir(filepath.Join(dir, ".semgrep"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := SemgrepCmd(Repo{WT: dir})
	if _, err := exec.LookPath("semgrep"); err != nil {
		if got != "" {
			t.Fatalf("sin semgrep instalado no corre, dio %q", got)
		}
		return
	}
	if !strings.Contains(got, "--baseline-commit "+Base) || !strings.Contains(got, "--error") {
		t.Fatalf("semgrep tiene que mirar solo lo nuevo y fallar con hallazgos: %q", got)
	}
}

func TestSoloInfra(t *testing.T) {
	puerto := "chequeo rojo en demo-frontend:\nError: http://localhost:8081/health is already used"
	bind := "chequeo rojo en ejemplo-frontend:\nlisten tcp :3200: bind: address already in use"
	rojo := "chequeo rojo en x:\n--- FAIL: TestCA1 (0.01s)"
	casos := []struct {
		d    Decision
		want bool
	}{
		{Decision{Motivos: []string{puerto, bind}}, true},
		{Decision{Motivos: []string{motivoCortado("x")}}, true},
		{Decision{Motivos: []string{puerto, rojo}}, false},
		{Decision{Motivos: []string{puerto}, Bloquean: []Hallazgo{{Regla: "sql-sin-parametros"}}}, false},
		{Decision{}, false},
		{Decision{Pasa: true, Motivos: []string{puerto}}, false},
		// El Builder que se cayó (timeout, la API) no gasta vuelta; el que no terminó por el código, sí.
		{Decide(1, Spec{}, Build{Bloqueo: "se cortó a los 30 min", Infra: true}, nil, nil, nil, nil), true},
		{Decide(1, Spec{}, Build{Bloqueo: "no encontré el endpoint"}, nil, nil, nil, nil), false},
	}
	for i, c := range casos {
		if got := SoloInfra(c.d); got != c.want {
			t.Errorf("caso %d: SoloInfra = %v, quería %v", i, got, c.want)
		}
	}
}

func TestSoloChequeo(t *testing.T) {
	golden := Decide(1, Spec{}, Build{Listo: true}, []Check{{Repo: "tienda", Salida: "+     ref: undefined,"}}, nil, nil, nil)
	casos := []struct {
		nombre string
		d      Decision
		want   bool
	}{
		{"chequeo rojo del golden", golden, true},
		{"Builder no terminó", Decide(1, Spec{}, Build{Bloqueo: "x"}, []Check{{Repo: "x", Salida: "FAIL"}}, nil, nil, nil), false},
		{"corte por tiempo: es del entorno", Decision{Motivos: []string{prefijoChequeoRojo + "x:\n" + motivoCortado("x")}}, false},
		{"con hallazgo que bloquea", Decision{Motivos: golden.Motivos, Bloquean: []Hallazgo{{Regla: "r"}}}, false},
		{"pasa", Decide(1, Spec{}, Build{Listo: true}, []Check{{Repo: "x", OK: true}}, nil, nil, nil), false},
	}
	for _, c := range casos {
		if got := SoloChequeo(c.d); got != c.want {
			t.Errorf("%s: %v, quería %v", c.nombre, got, c.want)
		}
	}
}

func TestRepetirPorInfra(t *testing.T) {
	puerto := Decision{Motivos: []string{"chequeo rojo en x:\nbind: address already in use"}}
	corte := Decision{Motivos: []string{motivoCortado("x")}}
	rojo := Decision{Motivos: []string{"chequeo rojo en x:\n--- FAIL: TestCA1"}}
	casos := []struct {
		nombre string
		d      Decision
		n      int
		want   bool
	}{
		{"puerto, primera", puerto, 0, true},
		{"puerto, segunda", puerto, 1, true},
		{"puerto, tope", puerto, maxRepeticionesInfra, false},
		{"corte, primera", corte, 0, true},
		{"corte, segunda: puede ser un test colgado", corte, 1, false},
		{"test rojo nunca", rojo, 0, false},
		{"Builder sin tiempo, segunda", Decision{Motivos: []string{motivoBuilderInfra + "salida de claude ilegible (context deadline exceeded)"}}, 1, false},
	}
	for _, c := range casos {
		if got := repetirPorInfra(c.d, c.n); got != c.want {
			t.Errorf("%s: %v, quería %v", c.nombre, got, c.want)
		}
	}
}

func TestTareaChica(t *testing.T) {
	spec := func(repos, criterios int, tipo string) Spec {
		s := Spec{Tipo: tipo, Repos: make([]string, repos)}
		s.Criterios = make([]struct {
			ID    string `json:"id"`
			Texto string `json:"texto"`
		}, criterios)
		return s
	}
	casos := []struct {
		nombre string
		s      Spec
		want   bool
	}{
		{"bug de un repo", spec(1, 3, "bug"), true},
		{"ajuste en el borde", spec(1, maxCriteriosChica, "ajuste"), true},
		{"feature", spec(1, 2, "feature"), false},
		{"dos repos", spec(2, 2, "bug"), false},
		{"muchos criterios", spec(1, maxCriteriosChica+1, "bug"), false},
	}
	for _, c := range casos {
		if got := tareaChica(c.s); got != c.want {
			t.Errorf("%s: tareaChica = %v, quería %v", c.nombre, got, c.want)
		}
	}
}

func TestJestFallidosAjenos(t *testing.T) {
	salida := "PASS a.test.ts\nFAIL features/mockups/components/Estudio.test.tsx\n  ● x\nTest Suites: 1 failed, 289 passed, 290 total\n"
	got := reJestFail.FindAllStringSubmatch(salida, -1)
	if len(got) != 1 || got[0][1] != "features/mockups/components/Estudio.test.tsx" {
		t.Fatalf("archivos FAIL: %v", got)
	}
	if !soloFallaronEsos(salida, []string{"features/mockups/components/Estudio.test.tsx"}) {
		t.Fatal("una sola suite roja y es esa: se puede excusar")
	}
	if soloFallaronEsos("FAIL a.test.ts\nTest Suites: 2 failed", []string{"a.test.ts"}) {
		t.Fatal("si falló otra suite (sin FAIL visible) no se excusa")
	}
}
