package fab

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
)

// Spec es la salida del Spec writer.
type Spec struct {
	Titulo    string   `json:"titulo"`
	Tipo      string   `json:"tipo"`
	Repos     []string `json:"repos"`
	Rutas     []string `json:"rutas"`
	Criterios []struct {
		ID    string `json:"id"`
		Texto string `json:"texto"`
	} `json:"criterios"`
	SpecMD string `json:"spec_md"`
	// Decisiones: lo que respondió el operador con `fabrica continuar`. Va aparte de SpecMD porque la
	// spec se le pasa a los expertos como dato no confiable y estas respuestas sí son órdenes suyas.
	Decisiones   string           `json:"decisiones,omitempty"`
	FaltaAclarar []map[string]any `json:"faltaAclarar"`
	Frenar       bool             `json:"frenar"`
	MotivoFrenar string           `json:"motivoFrenar"`
}

// Build es la salida del Builder.
type Build struct {
	Resumen         string           `json:"resumen"`
	TestsAceptacion []TestAceptacion `json:"tests_aceptacion"`
	Listo           bool             `json:"listo"`
	Bloqueo         string           `json:"bloqueo"`
	// Infra: el Builder no entregó porque se cayó el proceso (timeout, la API, salida ilegible), no
	// por el código. Lo pone la fábrica; queda en 02-build-vN.json para que replay decida igual.
	Infra bool `json:"infra,omitempty"`
}

// Hallazgo es un ítem de un revisor.
type Hallazgo struct {
	Revisor     string `json:"revisor"`
	Fingerprint string `json:"fingerprint"`
	Severidad   string `json:"severidad"`
	Regla       string `json:"regla"`
	Repo        string `json:"repo"`
	Archivo     string `json:"archivo"`
	Linea       int    `json:"linea"`
	CA          string `json:"ca,omitempty"`
	Evidencia   string `json:"evidencia"`
	Detalle     string `json:"detalle"`
	Confianza   int    `json:"confianza"`
	// DelPrompt lo pone el pipeline cuando la tarea la cargó el operador desde la terminal: ahí el texto
	// no es ajeno y una «instrucción en la entrada» se confirma, no bloquea. No viaja en el JSON.
	DelPrompt bool `json:"-"`
}

// Review es la salida de un revisor; Err != "" si no entregó.
type Review struct {
	Revisor   string     `json:"revisor"`
	Veredicto string     `json:"veredicto"`
	Hallazgos []Hallazgo `json:"hallazgos"`
	// Verificado: qué miró y cómo. Seguridad y código sin esto no entregaron (un "ok" vacío no vale).
	Verificado []string `json:"verificado,omitempty"`
	Err        string   `json:"error,omitempty"`
}

// Check es el resultado del chequeo local de un repo.
type Check struct {
	Repo   string `json:"repo"`
	OK     bool   `json:"ok"`
	Salida string `json:"salida"`
}

// Decision es 04-approver-vN.json.
type Decision struct {
	Pasa       bool       `json:"pasa"`
	Vuelta     int        `json:"vuelta"`
	Motivos    []string   `json:"motivos"`
	Bloquean   []Hallazgo `json:"bloquean"`
	Descartado []Hallazgo `json:"descartados"` // bajo el umbral: quedan a la vista, no bloquean
	// AConfirmar: graves pero sin evidencia. No vuelven al Builder (no le queman la vuelta):
	// los mira el operador antes de la aprobación.
	AConfirmar []Hallazgo `json:"a_confirmar,omitempty"`
	// Persisten: huellas que bloqueaban en la vuelta anterior y siguen. Con eso la tarea se traba
	// nombrando qué no se pudo arreglar.
	Persisten []string `json:"persisten,omitempty"`
	// SinRevisar: revisores que no corrieron por infraestructura, no por el código. Se ven en la aprobación.
	SinRevisar []string `json:"sin_revisar,omitempty"`
	// NuevosMenores: aparecieron en esta vuelta y no bloquearon por nuevos; si se repiten, bloquean.
	NuevosMenores []Hallazgo `json:"nuevos_menores,omitempty"`
	// Revisados: el commit de cada repo fijado antes del escaneo y las revisiones. Solo eso se aprueba.
	Revisados map[string]string `json:"revisados,omitempty"`
}

// Fingerprint identifica un hallazgo entre vueltas sin depender del texto.
func Fingerprint(h Hallazgo) string {
	s := sha1.Sum([]byte(h.Revisor + "|" + h.Regla + "|" + h.Repo + "|" + h.Archivo))
	return hex.EncodeToString(s[:6])
}

// claveArchivo identifica "lo que vio este revisor en este archivo" aunque en la otra vuelta le haya
// puesto otro nombre a la regla.
func claveArchivo(h Hallazgo) string { return "archivo:" + h.Revisor + "|" + h.Repo + "|" + h.Archivo }

// HuellasPrevias: lo que bloqueaba en esta vuelta, más lo que se dejó pasar por nuevo, para la
// siguiente (huella y clave por archivo): si un nuevo menor se repite en la vuelta siguiente, bloquea.
func HuellasPrevias(bloquean []Hallazgo, nuevos ...[]Hallazgo) map[string]bool {
	m := map[string]bool{}
	for _, grupo := range append([][]Hallazgo{bloquean}, nuevos...) {
		for _, h := range grupo {
			m[h.Fingerprint], m[claveArchivo(h)] = true, true
		}
	}
	return m
}

// medioArreglable: un MEDIUM de un revisor, con evidencia, archivo y confianza ≥ 6, fuera de un test de
// aceptación (quedan congelados después de la primera vuelta). Vuelve una vez al Builder. Las
// herramientas (gitleaks genérico, semgrep de confianza baja) no: son ruido y quemarían la vuelta.
func medioArreglable(h Hallazgo) bool {
	return h.Severidad == "MEDIUM" && strings.TrimSpace(h.Evidencia) != "" && h.Archivo != "" &&
		h.Confianza >= confianzaMedioArreglable && !strings.HasPrefix(h.Revisor, "herramienta:") && !IsAcceptanceTest(h.Archivo)
}

const confianzaMedioArreglable = 6

// motivoBuilderInfra: el Builder se cayó por el entorno. infraRe lo reconoce: no gasta vuelta.
const motivoBuilderInfra = "el Builder se cayó por el entorno, no por el código: "

// prefijoChequeoRojo: el motivo de un chequeo rojo. SoloChequeo lo reconoce para repetir sin contar.
const prefijoChequeoRojo = "chequeo rojo en "

// reglaInstruccion: el revisor marca así un texto de la entrada que le da órdenes a los agentes.
const reglaInstruccion = "instruccion-en-la-entrada"

// Decide es el Approver determinista: un LLM nunca aprueba solo. Tres reglas y nada más (05/11: cada
// regla extra trababa tareas con el código bien; menos burocracia):
//  1. el Builder terminó y el chequeo del repo (su make check) está verde;
//  2. ningún hallazgo HIGH o CRITICAL con evidencia (de un revisor o de una herramienta);
//  3. un MEDIUM de un revisor con evidencia y archivo (medioArreglable) frena solo en la primera vuelta: el Builder lo arregla una vez y,
//     si sigue en la última, pasa a «a confirmar» (nunca traba la tarea). El 16/11 tres MEDIUM reales de
//     6a3d (un error tragado que pisaba adjuntos) llegaron a la aprobación sin que nadie los arreglara;
//  4. lo demás —MEDIUM en la última vuelta, sin evidencia, una instrucción en una tarea que cargó el operador, un criterio sin test declarado,
//     un revisor que no entregó— no frena: queda a la vista antes de la aprobación (y apaga el merge solo).
//
// previos: las huellas que bloqueaban en la vuelta anterior, para nombrar lo que no se pudo arreglar.
func Decide(vuelta int, spec Spec, build Build, checks []Check, reviews []Review, _ []string, previos map[string]bool) Decision {
	d := Decision{Vuelta: vuelta}
	switch {
	case !build.Listo && build.Infra:
		d.Motivos = append(d.Motivos, motivoBuilderInfra+build.Bloqueo)
	case !build.Listo:
		d.Motivos = append(d.Motivos, "el Builder no terminó: "+build.Bloqueo)
	}
	for _, c := range checks {
		if !c.OK {
			d.Motivos = append(d.Motivos, fmt.Sprintf("%s%s:\n%s", prefijoChequeoRojo, c.Repo, c.Salida))
		}
	}
	covered := map[string]bool{}
	for _, t := range build.TestsAceptacion {
		covered[t.CA] = true
	}
	for _, ca := range spec.Criterios {
		if !covered[ca.ID] {
			d.AConfirmar = append(d.AConfirmar, Hallazgo{Revisor: "approver", Severidad: "MEDIUM", Regla: "ca-sin-test", Detalle: ca.ID + " no tiene test de aceptación declarado"})
		}
	}
	d.AConfirmar = append(d.AConfirmar, dudasDeAlcance(spec)...)
	for _, r := range reviews {
		if r.Err != "" {
			d.SinRevisar = append(d.SinRevisar, r.Revisor+": "+strings.TrimPrefix(r.Err, infraPrefijo))
			continue
		}
		for _, h := range r.Hallazgos {
			grave := h.Severidad == "CRITICAL" || h.Severidad == "HIGH"
			conEvidencia := strings.TrimSpace(h.Evidencia) != ""
			switch {
			case h.Regla == reglaInstruccion && h.DelPrompt:
				// El texto lo cargó el operador por la consola: no es una inyección (be1d). En un ticket o
				// un log sí puede serlo, y ahí sigue frenando como cualquier HIGH con evidencia.
				d.AConfirmar = append(d.AConfirmar, h)
			case grave && conEvidencia:
				d.Bloquean = append(d.Bloquean, h)
				if previos[h.Fingerprint] {
					d.Persisten = append(d.Persisten, fmt.Sprintf("%s %s en %s/%s (%s)", h.Revisor, h.Regla, h.Repo, h.Archivo, h.Fingerprint))
				}
			case vuelta < maxVueltas && medioArreglable(h):
				d.Bloquean = append(d.Bloquean, h)
			case grave || h.Severidad == "MEDIUM":
				d.AConfirmar = append(d.AConfirmar, h)
			default:
				d.Descartado = append(d.Descartado, h)
			}
		}
	}
	d.Pasa = len(d.Motivos) == 0 && len(d.Bloquean) == 0
	return d
}

// dudasDeAlcance: lo que el Spec writer decidió solo sobre qué recibe el cliente queda a la vista
// antes de la aprobación (y apaga el merge solo). Si el operador ya respondió con `continuar`, no se repite.
func dudasDeAlcance(spec Spec) []Hallazgo {
	if strings.TrimSpace(spec.Decisiones) != "" {
		return nil
	}
	var hs []Hallazgo
	for _, duda := range spec.FaltaAclarar {
		if duda["impacto"] != "alcance" {
			continue
		}
		hs = append(hs, Hallazgo{Revisor: "spec-writer", Severidad: "MEDIUM", Regla: "duda-de-alcance",
			Detalle: fmt.Sprintf("%v Asumido: %v", duda["pregunta"], duda["defaultRecomendado"])})
	}
	return hs
}

// Feedback es lo que vuelve al Builder: qué falla y dónde, ítem por ítem.
func (d Decision) Feedback() string {
	var b strings.Builder
	for _, m := range d.Motivos {
		fmt.Fprintf(&b, "- %s\n", m)
	}
	for _, h := range d.Bloquean {
		fmt.Fprintf(&b, "- [%s %s] %s/%s:%d %s\n  evidencia: %s\n", h.Revisor, h.Severidad, h.Repo, h.Archivo, h.Linea, h.Detalle, h.Evidencia)
	}
	b.WriteString("Está listo cuando el chequeo queda verde y cada ítem de arriba está resuelto.\n")
	return b.String()
}

// TestAceptacion es lo que el Builder declara por cada criterio (va al cuerpo de la PR y lo mira el
// revisor de código). Verde es el chequeo del repo y, en la PR, el CI.
type TestAceptacion struct {
	CA      string `json:"ca"`
	Repo    string `json:"repo"`
	Archivo string `json:"archivo"`
	Test    string `json:"test"`
}
