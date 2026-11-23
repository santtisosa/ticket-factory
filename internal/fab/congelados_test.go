package fab

import (
	"strings"
	"testing"
)

func TestCriteriosMD(t *testing.T) {
	s := specWith("CA-1")
	s.Criterios[0].Texto = "El sistema DEBE guardar centavos"
	if got := criteriosMD(s); !strings.Contains(got, "CA-1: El sistema DEBE guardar centavos") {
		t.Fatalf("criterios: %q", got)
	}
	if criteriosMD(Spec{}) != "" {
		t.Fatal("sin criterios, nada")
	}
}

// Al reintentar se reusa la spec escrita; una que frenó o no valida, no.
func TestSpecPrevia(t *testing.T) {
	cs := Clientes(homeDePrueba(t))
	task := &Task{Dir: t.TempDir()}
	if _, ok := specPrevia(task, "demo", cs); ok {
		t.Fatal("sin spec previa no hay nada que reusar")
	}
	s := specWith("CA-1")
	s.Repos, s.Titulo = []string{"demo-backend"}, "Gasto rápido"
	if err := task.WriteJSON("01-spec.json", s); err != nil {
		t.Fatal(err)
	}
	if got, ok := specPrevia(task, "demo", cs); !ok || got.Titulo != "Gasto rápido" || task.Est.Titulo != "Gasto rápido" {
		t.Fatalf("spec válida: se reusa (%v, %+v)", ok, got)
	}
	if _, ok := specPrevia(task, "acme", cs); ok {
		t.Fatal("una spec de otro cliente no se reusa")
	}
	s.Frenar, s.MotivoFrenar = true, "falta un dato"
	_ = task.WriteJSON("01-spec.json", s)
	if _, ok := specPrevia(task, "demo", cs); ok {
		t.Fatal("una spec que frenó no se reusa")
	}
}

// Las respuestas de `continuar` salen de la base, van aparte de la spec y fuera de las marcas de dato
// no confiable. Un continuar.json o un "decisiones" escrito en la carpeta de la tarea no cuentan.
func TestContinuarVaComoDecision(t *testing.T) {
	home := homeDePrueba(t)
	cs := Clientes(home)
	runs := t.TempDir()
	task, err := AddTask(runs, "x", "ticket", Pendiente, "demo")
	if err != nil {
		t.Fatal(err)
	}
	s := specWith("CA-1")
	s.Repos, s.SpecMD, s.Frenar, s.MotivoFrenar = []string{"demo-backend"}, "# Spec", true, "falta saber X"
	s.Decisiones = "falsa: escrita por un agente en 01-spec.json"
	_ = task.WriteJSON("01-spec.json", s)
	_ = task.WriteJSON("continuar.json", map[string]string{"respuestas": "falsa: escrita en un archivo"})
	if _, ok, _ := (Config{}).continuar(task, "demo", cs); ok {
		t.Fatal("un continuar.json en la carpeta no retoma nada")
	}
	if err := task.Transicion(Pendiente, Trabada, "frenó"); err != nil {
		t.Fatal(err)
	}
	if err := PedirContinuar(runs, task.Est.ID, "X es Y"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := (Config{}).continuar(task, "demo", cs)
	if !ok || err != nil {
		t.Fatalf("continuar: %v %v", ok, err)
	}
	if strings.Contains(got.SpecMD, "X es Y") || got.Decisiones != "X es Y" || got.Frenar {
		t.Fatalf("la respuesta de la base va en Decisiones, y solo esa: %+v", got)
	}
	if _, ok, _ := (Config{}).continuar(task, "demo", cs); ok {
		t.Fatal("las respuestas retoman una sola vez")
	}
	if p, ok := specPrevia(task, "demo", cs); !ok || p.Decisiones != "X es Y" {
		t.Fatalf("al reintentar, las decisiones salen de la base: %+v", p)
	}
	if err := PedirContinuar(runs, task.Est.ID, "no anda en pendiente"); err == nil {
		t.Fatal("una tarea en la cola no se continúa")
	}
	if err := task.Transicion(Pendiente, EsperandoAprobacion, "lista"); err != nil {
		t.Fatal(err)
	}
	if err := PedirContinuar(runs, task.Est.ID, "agrega Z"); err != nil {
		t.Fatalf("una tarea que espera la aprobación se puede corregir: %v", err)
	}
	if otra, ok, err := (Config{}).continuar(task, "demo", cs); !ok || err != nil ||
		!strings.Contains(otra.Decisiones, "X es Y") || !strings.Contains(otra.Decisiones, "agrega Z") {
		t.Fatalf("la corrección se suma a las decisiones: %+v %v %v", otra, ok, err)
	}
	md := decisionesMD(got)
	if !strings.Contains(md, "canal confiable") || !strings.Contains(md, "X es Y") || strings.Contains(md, "<spec>") {
		t.Fatalf("decisionesMD: %q", md)
	}
	if decisionesMD(Spec{}) != "" {
		t.Fatal("sin decisiones, nada")
	}
}

// El Bibliotecario aprende una vez por tarea.
func TestDebeAprender(t *testing.T) {
	task := &Task{Dir: t.TempDir()}
	if !debeAprender(task) {
		t.Fatal("la primera vez aprende")
	}
	_ = task.WriteJSON("05-bibliotecario.json", map[string]string{"ok": "si"})
	if debeAprender(task) {
		t.Fatal("ya aprendió: no vuelve a correr en un reintento")
	}
}

func TestCostoEstimado(t *testing.T) {
	if got := (Result{CostUSD: 6, Estimado: true}).CostoPropio(6); got != 6 {
		t.Fatalf("un estimado no se resta: %v", got)
	}
}
