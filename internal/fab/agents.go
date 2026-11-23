package fab

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type agentDef struct {
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Model       string `json:"model,omitempty"`
	// Effort no va en el JSON de --agents: se pasa siempre como --effort (y el modelo como --model),
	// porque no está documentado que --agent X respete el modelo de su definición.
	Effort string `json:"-"`
}

// Modelo es lo que corre un experto: modelo y effort explícitos. Vacío = el de la cuenta.
type Modelo struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// archivoModelos: al lado del JSON de --agents, el modelo y el effort de cada experto.
func archivoModelos(agentsFile string) string { return agentsFile + ".modelos.json" }

// ModeloDe lee el modelo y el effort de un experto (vacío si no está o no se pudo leer).
func ModeloDe(agentsFile, agent string) Modelo {
	var m map[string]Modelo
	b, err := os.ReadFile(archivoModelos(agentsFile))
	if err != nil || json.Unmarshal(b, &m) != nil {
		return Modelo{}
	}
	return m[agent]
}

// LoadAgents lee definiciones .md (frontmatter + cuerpo) y las escribe como el JSON de --agents.
// Así los agentes que ya existen se referencian, no se copian: los del proyecto no se encuentran
// solos cuando el cwd es un repo cliente.
func LoadAgents(paths map[string]string, out string) error {
	defs := map[string]agentDef{}
	for name, p := range paths {
		d, err := parseAgent(p)
		if err != nil {
			return fmt.Errorf("agente %s: %w", name, err)
		}
		defs[name] = d
	}
	b, err := json.MarshalIndent(defs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return err
	}
	modelos := map[string]Modelo{}
	for name, d := range defs {
		modelos[name] = Modelo{Model: d.Model, Effort: d.Effort}
	}
	if b, err = json.MarshalIndent(modelos, "", "  "); err != nil {
		return err
	}
	return os.WriteFile(archivoModelos(out), b, 0o644)
}

func parseAgent(path string) (agentDef, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return agentDef{}, err
	}
	parts := strings.SplitN(string(raw), "---", 3)
	if len(parts) != 3 {
		return agentDef{}, fmt.Errorf("%s: sin frontmatter", path)
	}
	d := agentDef{Prompt: strings.TrimSpace(parts[2])}
	for line := range strings.SplitSeq(parts[1], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "description":
			d.Description = v
		case "model":
			if v != "inherit" {
				d.Model = v
			}
		case "effort":
			d.Effort = v
		}
		// El `tools:` del frontmatter no se copia: en agents.json es una lista cerrada que le sacaba
		// a UX el Playwright del --mcp-config y a UI el git diff (y a todos StructuredOutput). Lo que
		// cada experto puede usar lo decide su llamada (--allowedTools con dontAsk, que niega todo lo
		// demás); el frontmatter lo documenta y un test (TestFrontmatterUX) lo mantiene igual para UX.
	}
	return d, nil
}
