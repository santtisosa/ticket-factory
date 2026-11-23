package fab

import (
	"strings"
	"testing"
)

// Replay: una vuelta trabada solo por un hallazgo sobre un test de aceptación ahora pasa; una con el
// chequeo rojo sigue sin pasar y queda contada como "de código".
func TestReplay(t *testing.T) {
	task := &Task{Dir: t.TempDir()}
	s := specWith("CA-1")
	s.Repos, s.Titulo = []string{"demo-backend"}, "x"
	_ = task.WriteJSON("01-spec.json", s)
	_ = task.WriteJSON("02-build-v1.json", buildCovering("CA-1"))
	_ = task.WriteJSON("02-build-v2.json", buildCovering("CA-1"))
	sobreTest := Hallazgo{Revisor: "codigo", Severidad: "MEDIUM", Confianza: 9, Evidencia: "x", Regla: "test-sin-dientes", Repo: "demo-backend", Archivo: "a.aceptacion.test.js"}
	_ = task.WriteJSON("04-approver-v1.json", Decision{Vuelta: 1, Pasa: false, Bloquean: []Hallazgo{sobreTest}})
	_ = task.WriteJSON("04-approver-v2.json", Decision{Vuelta: 2, Pasa: false, Motivos: []string{"chequeo rojo en demo-backend:\nFAIL x"}})
	vs, err := ReplayTarea(task.Dir)
	if err != nil || len(vs) != 2 {
		t.Fatalf("replay: %v %v", vs, err)
	}
	if vs[0].Antes || !vs[0].Ahora {
		t.Fatalf("v1 ahora pasa: %+v", vs[0])
	}
	if vs[1].Ahora || len(vs[1].MotivosDeCodigo) != 1 {
		t.Fatalf("v2 sigue roja por código: %+v", vs[1])
	}
	tabla := TablaReplay(vs)
	if !strings.Contains(tabla, "1 pasan ahora y antes no") || !strings.Contains(tabla, "1 frenan solo por código") {
		t.Fatalf("tabla: %s", tabla)
	}
}

// Una corrida nueva archiva la anterior en corrida-N/ (la spec y la sesión quedan).
func TestArchivarCorrida(t *testing.T) {
	task := &Task{Dir: t.TempDir()}
	_ = task.WriteJSON("01-spec.json", Spec{})
	_ = task.WriteJSON("02-build-v1.json", Build{})
	_ = task.WriteJSON("04-approver-v1.json", Decision{})
	_ = task.WriteJSON("03-builder.sesion.json", sesion{ID: "x"})
	if err := archivarCorrida(task); err != nil {
		t.Fatal(err)
	}
	var d Decision
	if task.ReadJSON("corrida-1/04-approver-v1.json", &d) != nil || task.ReadJSON("04-approver-v1.json", &d) == nil {
		t.Fatal("la decisión pasa a corrida-1/")
	}
	var s Spec
	if task.ReadJSON("01-spec.json", &s) != nil {
		t.Fatal("la spec queda (se reusa)")
	}
	_ = task.WriteJSON("02-build-v1.json", Build{})
	if err := archivarCorrida(task); err != nil {
		t.Fatal(err)
	}
	var b Build
	if task.ReadJSON("corrida-2/02-build-v1.json", &b) != nil {
		t.Fatal("la segunda corrida va a corrida-2/")
	}
	if err := archivarCorrida(task); err != nil {
		t.Fatal("sin nada que mover no hace nada")
	}
}

func TestClaseDeFreno(t *testing.T) {
	casos := map[string]string{
		"spec: el Spec writer frenó: falta el precio":          "pide una decisión",
		"2 vueltas sin pasar; ver 04-approver-v2.json":         "vueltas sin pasar",
		"worktrees: clonar x: exit 128":                        "infraestructura",
		"no pude guardar lo aprobado: git rev-parse: exit 128": "otro",
	}
	for m, quiere := range casos {
		if got := ClaseDeFreno(m); got != quiere {
			t.Errorf("%q: %q, quería %q", m, got, quiere)
		}
	}
}
