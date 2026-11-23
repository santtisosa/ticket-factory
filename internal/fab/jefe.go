package fab

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Cita es una línea del cerebro que sostiene una respuesta del Jefe.
type Cita struct {
	Archivo string `json:"archivo"`
	Linea   int    `json:"linea"`
	Texto   string `json:"texto"`
}

// Respuesta del Jefe, ya con las citas verificadas.
type Respuesta struct {
	Sabe        bool   `json:"sabe"`
	Respuesta   string `json:"respuesta"`
	Citas       []Cita `json:"citas"`
	Descartadas []Cita `json:"descartadas,omitempty"`
	Aviso       string `json:"aviso,omitempty"`
}

const (
	citaVentana = 3  // líneas de tolerancia alrededor de la línea citada
	citaMinima  = 12 // caracteres: menos que eso no identifica una regla
)

// VerifyCitas deja solo las citas que existen de verdad: el archivo está dentro del cerebro, la
// línea existe y el texto aparece a ±3 líneas. Sin modelo: es un script.
func VerifyCitas(root string, r Respuesta) Respuesta {
	var ok, bad []Cita
	for _, c := range r.Citas {
		if citaExiste(root, c) {
			ok = append(ok, c)
		} else {
			bad = append(bad, c)
		}
	}
	r.Citas, r.Descartadas = ok, bad
	if r.Sabe && len(ok) == 0 {
		r.Sabe, r.Aviso = false, "sin citas verificadas: tomalo como no sabe"
	}
	return r
}

func citaExiste(root string, c Cita) bool {
	want := strings.TrimSpace(c.Texto)
	// Una cita corta ("ver", "## Reglas") aparece en cualquier lado: no sostiene nada.
	if len([]rune(want)) < citaMinima || c.Linea < 1 {
		return false
	}
	p := filepath.Join(root, filepath.Clean("/"+c.Archivo)) // Clean con "/" impide salir con ../
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		if n < c.Linea-citaVentana {
			continue
		}
		if n > c.Linea+citaVentana {
			break
		}
		if strings.Contains(sc.Text(), want) {
			return true
		}
	}
	return false
}

// Ask le pregunta al Jefe. El costo va a jefe.jsonl, que suma al tope diario.
func (c Config) Ask(ctx context.Context, pregunta string) (Respuesta, error) {
	soltar, err := c.bloquearCerebro()
	if err != nil {
		return Respuesta{}, err
	}
	defer soltar()
	if err := c.EnsureBrain(ctx); err != nil {
		return Respuesta{}, err
	}
	if _, err := c.BuildIndex(); err != nil {
		return Respuesta{}, err
	}
	r, err := Run(ctx, Call{
		Dir: c.dirAgentes(), AddDirs: []string{c.BrainFarm()}, Agent: "jefe", AgentsFile: c.AgentsFile, SettingsFile: c.SettingsFile,
		Schema: schema("jefe.json"), AllowedTools: []string{"Read", "Grep", "Glob", "StructuredOutput"},
		BudgetUSD: c.BudgetCallUSD, Timeout: expertTimeout,
		Prompt: carpetaCerebro(c.BrainFarm()) + "Pregunta: " + pregunta,
	})
	c.logJefe(pregunta, r.CostUSD, err)
	if err != nil {
		return Respuesta{}, err
	}
	var resp Respuesta
	if err := json.Unmarshal(r.Structured, &resp); err != nil {
		return Respuesta{}, err
	}
	return VerifyCitas(c.BrainFarm(), resp), nil
}

func (c Config) logJefe(pregunta string, cost float64, err error) {
	estado := "ok"
	if err != nil {
		estado = "falla"
	}
	LogLado(c.Runs, "jefe", "jefe", estado, pregunta, cost)
}
