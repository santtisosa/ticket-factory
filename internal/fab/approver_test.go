package fab

import (
	"encoding/json"
	"strings"
	"testing"
)

func specWith(ids ...string) Spec {
	var s Spec
	for _, id := range ids {
		s.Criterios = append(s.Criterios, struct {
			ID    string `json:"id"`
			Texto string `json:"texto"`
		}{ID: id})
	}
	return s
}

func buildCovering(ids ...string) Build {
	var b Build
	b.Listo = true
	raw, _ := json.Marshal(map[string]any{"tests_aceptacion": func() []map[string]string {
		var t []map[string]string
		for _, id := range ids {
			t = append(t, map[string]string{"ca": id, "repo": "demo-backend", "archivo": "x_acceptance_test.go", "test": "T"})
		}
		return t
	}()})
	_ = json.Unmarshal(raw, &b)
	return b
}

func TestFingerprintIgnoraElTexto(t *testing.T) {
	a := Hallazgo{Revisor: "codigo", Regla: "dup", Repo: "r", Archivo: "f.go", Linea: 3, Detalle: "uno"}
	b := a
	b.Linea, b.Detalle = 40, "otro texto"
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("el mismo hallazgo con otro texto o línea tiene que tener la misma huella")
	}
	b.Archivo = "g.go"
	if Fingerprint(a) == Fingerprint(b) {
		t.Fatal("otro archivo es otro hallazgo")
	}
}

func TestParseAgentLeePromptYDescripcion(t *testing.T) {
	d, err := parseAgent("../../agents/builder.md")
	if err != nil {
		t.Fatal(err)
	}
	if d.Prompt == "" || d.Description == "" {
		t.Fatalf("definición incompleta: %+v", d)
	}
}

func TestCambiosDeCI(t *testing.T) {
	hs := cambiosDeCI("acme", []string{"index.html", ".github/workflows/ci.yml"})
	if len(hs) != 1 || hs[0].Archivo != ".github/workflows/ci.yml" {
		t.Fatalf("solo .github/: %+v", hs)
	}
	d := Decide(1, specWith("CA-1"), buildCovering("CA-1"), []Check{{Repo: "acme", OK: true}}, []Review{{Revisor: revisorHerramienta + "ci", Veredicto: "ok", Hallazgos: hs}}, nil, nil)
	if !d.Pasa || len(d.AConfirmar) != 1 {
		t.Fatalf("pasa, con el cambio de CI a confirmar: %+v", d)
	}
}

// bloquea: el hallazgo solo, en una vuelta con todo lo demás verde, frena la tarea.
func bloquea(h Hallazgo) bool {
	d := Decide(1, specWith("CA-1"), buildCovering("CA-1"), []Check{{Repo: "r", OK: true}}, []Review{{Revisor: h.Revisor, Hallazgos: []Hallazgo{h}}}, nil, nil)
	return !d.Pasa
}

func TestDecideTresReglas(t *testing.T) {
	verde := []Check{{Repo: "r", OK: true}}
	casos := []struct {
		nombre string
		spec   Spec
		build  Build
		checks []Check
		revs   []Review
		pasa   bool
	}{
		{"todo verde pasa", specWith("CA-1"), buildCovering("CA-1"), verde, nil, true},
		{"chequeo rojo frena", specWith("CA-1"), buildCovering("CA-1"), []Check{{Repo: "r", Salida: "FAIL"}}, nil, false},
		{"el Builder no terminó frena", specWith("CA-1"), Build{}, verde, nil, false},
		{"HIGH con evidencia frena", specWith("CA-1"), buildCovering("CA-1"), verde, []Review{{Revisor: "codigo", Hallazgos: []Hallazgo{{Revisor: "codigo", Severidad: "HIGH", Evidencia: "a.go:3"}}}}, false},
		{"HIGH sin evidencia no frena", specWith("CA-1"), buildCovering("CA-1"), verde, []Review{{Revisor: "seguridad", Hallazgos: []Hallazgo{{Revisor: "seguridad", Severidad: "HIGH"}}}}, true},
		{"MEDIUM con evidencia no frena", specWith("CA-1"), buildCovering("CA-1"), verde, []Review{{Revisor: "seguridad", Hallazgos: []Hallazgo{{Revisor: "seguridad", Severidad: "MEDIUM", Evidencia: "x"}}}}, true},
		{"instrucción en una tarea del operador no frena", specWith("CA-1"), buildCovering("CA-1"), verde, []Review{{Revisor: "codigo", Hallazgos: []Hallazgo{{Revisor: "codigo", Severidad: "HIGH", Evidencia: "x", Regla: reglaInstruccion, DelPrompt: true}}}}, true},
		{"instrucción en un ticket frena", specWith("CA-1"), buildCovering("CA-1"), verde, []Review{{Revisor: "codigo", Hallazgos: []Hallazgo{{Revisor: "codigo", Severidad: "HIGH", Evidencia: "x", Regla: reglaInstruccion}}}}, false},
		{"criterio sin test no frena", specWith("CA-1", "CA-2"), buildCovering("CA-1"), verde, nil, true},
		{"revisor que no entregó no frena", specWith("CA-1"), buildCovering("CA-1"), verde, []Review{{Revisor: "codigo", Err: "se cayó"}}, true},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			d := Decide(2, c.spec, c.build, c.checks, c.revs, nil, nil)
			if d.Pasa != c.pasa {
				t.Fatalf("pasa=%v, quería %v: %+v", d.Pasa, c.pasa, d)
			}
		})
	}
	if bloquea(Hallazgo{Revisor: "codigo", Severidad: "MEDIUM", Evidencia: "x", Archivo: "a.js", Confianza: 5}) {
		t.Fatal("un MEDIUM de confianza 5 no vuelve al Builder")
	}
	// MEDIUM con evidencia y archivo: vuelve al Builder en la primera vuelta, nunca en la última (6a3d, 16/11).
	medio := func(archivo string) []Review {
		return []Review{{Revisor: "codigo", Hallazgos: []Hallazgo{{Revisor: "codigo", Severidad: "MEDIUM", Evidencia: "x", Archivo: archivo, Linea: 471, Confianza: 6}}}}
	}
	for _, c := range []struct {
		nombre  string
		vuelta  int
		archivo string
		pasa    bool
	}{
		{"MEDIUM con archivo en la primera vuelta frena", 1, "js/acme-data.js", false},
		{"MEDIUM con archivo en la última vuelta no frena", maxVueltas, "js/acme-data.js", true},
		{"MEDIUM sin archivo en la primera vuelta no frena", 1, "", true},
		{"MEDIUM sobre un test de aceptación no frena", 1, "test/crm-adjuntos.aceptacion.test.js", true},
	} {
		if d := Decide(c.vuelta, specWith("CA-1"), buildCovering("CA-1"), verde, medio(c.archivo), nil, nil); d.Pasa != c.pasa {
			t.Fatalf("%s: pasa=%v, quería %v: %+v", c.nombre, d.Pasa, c.pasa, d)
		}
	}

	d := Decide(2, specWith("CA-1", "CA-2"), buildCovering("CA-1"), verde, []Review{{Revisor: "seguridad", Hallazgos: []Hallazgo{{Revisor: "seguridad", Severidad: "MEDIUM", Evidencia: "x"}}}}, nil, nil)
	if len(d.AConfirmar) != 2 {
		t.Fatalf("lo que no frena queda a la vista antes de la aprobación: %+v", d.AConfirmar)
	}

	conDudas := specWith("CA-1")
	conDudas.FaltaAclarar = []map[string]any{
		{"pregunta": "¿También en X?", "defaultRecomendado": "No", "impacto": "alcance"},
		{"pregunta": "¿Qué ícono?", "defaultRecomendado": "El ojo", "impacto": "bajo"},
	}
	d = Decide(1, conDudas, buildCovering("CA-1"), verde, nil, nil, nil)
	if !d.Pasa || len(d.AConfirmar) != 1 || d.AConfirmar[0].Regla != "duda-de-alcance" || !strings.Contains(d.AConfirmar[0].Detalle, "¿También en X?") {
		t.Fatalf("solo la duda de alcance queda a confirmar, y no frena: %+v", d)
	}
	conDudas.Decisiones = "sí, también en X"
	if d = Decide(1, conDudas, buildCovering("CA-1"), verde, nil, nil, nil); len(d.AConfirmar) != 0 {
		t.Fatalf("lo que el operador ya respondió no se repite: %+v", d.AConfirmar)
	}
}
