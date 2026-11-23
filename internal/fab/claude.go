package fab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Call es una corrida de un experto: un proceso `claude -p` aislado, con contexto limpio.
type Call struct {
	Dir          string // cwd del proceso: Config.dirAgentes, nunca un worktree ni un repo (cwdSeguro)
	Agent        string // nombre dentro de AgentsFile
	AgentsFile   string // JSON generado por LoadAgents
	SettingsFile string // hooks propios de la fábrica
	Prompt       string
	Schema       []byte   // JSON Schema de la salida
	AllowedTools []string // con dontAsk, lo que no está acá se niega solo
	AddDirs      []string
	MCPConfig    string // vacío: ningún MCP (ni los del usuario)
	Env          []string
	BudgetUSD    float64
	Modelo       Modelo // --model y --effort explícitos; vacío = los de la cuenta
	// Sesión: con SessionID la corrida queda guardada y Resume la retoma (el Builder en la vuelta 2).
	// CostoPrevio es lo que ya se contó de esa sesión: el total que informa claude es acumulado.
	SessionID   string
	Resume      bool
	CostoPrevio float64
	Timeout     time.Duration
	Rastro      string // archivo del --debug-file: qué herramientas usó y qué le negaron, para auditar
}

// Result es lo que importa del JSON de `claude -p --output-format json`.
type Result struct {
	IsError    bool            `json:"is_error"`
	Subtype    string          `json:"subtype"`
	NumTurns   int             `json:"num_turns"`
	CostUSD    float64         `json:"total_cost_usd"`
	Structured json.RawMessage `json:"structured_output"`
	Text       string          `json:"result"`
	// Estimado: no hubo salida y CostUSD es el tope de la llamada, no lo que informó claude.
	Estimado bool `json:"-"`
	// Denials son las herramientas que se negaron (dontAsk, allowedTools o un hook).
	Denials []struct {
		Tool  string         `json:"tool_name"`
		Input map[string]any `json:"tool_input"`
	} `json:"permission_denials"`
}

// DenialSummary resume lo negado para el log ("Write notas/x.md; Bash git push").
func (r Result) DenialSummary() string {
	var parts []string
	for _, d := range r.Denials {
		arg := ""
		for _, k := range []string{"file_path", "command", "pattern"} {
			if v, ok := d.Input[k]; ok {
				arg = fmt.Sprint(v)
				break
			}
		}
		parts = append(parts, strings.TrimSpace(d.Tool+" "+arg))
	}
	return strings.Join(parts, "; ")
}

// CostoPropio: lo que gastó esta corrida (en una sesión retomada, el total menos lo ya contado).
func (r Result) CostoPropio(previo float64) float64 {
	if r.Estimado {
		return r.CostUSD // sin salida se cuenta el tope entero: no es un acumulado al que restarle
	}
	if c := r.CostUSD - previo; c > 0 {
		return c
	}
	return 0
}

// ErrSinSalida: el experto terminó sin entregar el JSON del esquema. Cuenta como "no pasa".
var ErrSinSalida = errors.New("el experto no entregó structured_output")

func (c Call) args() []string {
	a := []string{"-p",
		"--agents", c.AgentsFile, "--agent", c.Agent,
		// Ni los settings del usuario ni los del proyecto: los del usuario cargaban 16 plugins y hooks
		// de 15 eventos en cada experto (un guardia que pedía "hechos" antes de editar le negaba
		// Write/Edit al Builder; ~4x de contexto fijo por llamada, medido el 01/11). Lo que vale
		// (guardias, modelo por defecto, attribution) va en SettingsFile.
		"--setting-sources", "local", "--settings", c.SettingsFile,
		"--permission-mode", "dontAsk", "--permission-prompts", "none",
		"--output-format", "json", "--json-schema", string(c.Schema),
	}
	switch {
	case c.Resume && c.SessionID != "":
		a = append(a, "--resume", c.SessionID)
	case c.SessionID != "":
		a = append(a, "--session-id", c.SessionID)
	default:
		a = append(a, "--no-session-persistence")
	}
	if c.Modelo.Model != "" {
		a = append(a, "--model", c.Modelo.Model)
	}
	if c.Modelo.Effort != "" {
		a = append(a, "--effort", c.Modelo.Effort)
	}
	// Solo los MCP que la fábrica le da a cada experto: menos ruido y menos tokens.
	if c.MCPConfig != "" {
		a = append(a, "--mcp-config", c.MCPConfig)
	}
	a = append(a, "--strict-mcp-config")
	if len(c.AllowedTools) > 0 {
		a = append(a, "--allowedTools", strings.Join(c.AllowedTools, ","))
	}
	for _, d := range c.AddDirs {
		a = append(a, "--add-dir", d)
	}
	if c.BudgetUSD > 0 {
		a = append(a, "--max-budget-usd", fmt.Sprintf("%.2f", c.BudgetUSD))
	}
	// ponytail: --max-turns no existe en este CLI (2.1.316); el tope es la plata y el timeout.
	if c.Rastro != "" {
		a = append(a, "--debug-file", c.Rastro)
	}
	return a
}

// Run lanza el experto y devuelve su salida estructurada. El costo vuelve aunque haya error.
func Run(ctx context.Context, c Call) (Result, error) {
	// C3: con --setting-sources local, Claude Code carga hooks del cwd (y de la raíz del repo git que
	// lo contenga) y -p los ejecuta sin pedir confianza. Antes de cada llamada, el cwd se verifica.
	if err := cwdSeguro(c.Dir); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInfra, err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", c.args()...)
	enGrupoSuave(cmd)
	cmd.Dir = c.Dir
	// Sin esperar tareas de fondo al salir: un `next dev` que dejó el experto lo colgaba hasta 10 min.
	cmd.Env = append(cleanEnv(), append(c.Env, "FABRICA=1", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1")...)
	cmd.Stdin = strings.NewReader(c.Prompt)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	matarGrupo(cmd) // ni un `next dev` ni otro proceso de fondo del experto sobreviven a la llamada

	var r Result
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		// Sin salida no hay costo informado, pero la llamada pudo gastar hasta su tope: se cuenta
		// el tope como estimado, para que el gasto del día no quede en cero por un timeout.
		r.CostUSD, r.Estimado = c.BudgetUSD, true
		return r, fmt.Errorf("salida de claude ilegible (%v): %s %s", runErr, tail(out.String()), tail(errb.String()))
	}
	if r.IsError || r.Subtype != "success" {
		return r, fmt.Errorf("claude terminó con %s: %s", r.Subtype, tail(r.Text))
	}
	if len(r.Structured) == 0 || string(r.Structured) == "null" {
		return r, ErrSinSalida
	}
	return r, nil
}

// envPermitido: lo único que un experto hereda del entorno de la fábrica. Es una lista blanca:
// los secretos del archivo env (BETTERSTACK_TOKEN, FABRICA_SMTP_CLAVE) y las variables de la
// sesión de Claude Code que lanzó la fábrica nunca llegan a un agente. Lo que necesita cada uno
// (FABRICA_CHECK, las variables de los servicios) se lo pasa la llamada en Call.Env.
var envPermitido = []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TERM", "LANG", "LC_ALL", "LC_CTYPE",
	"FABRICA_HOME", "GOROOT", "GOFLAGS", "NVM_DIR", "NODE_OPTIONS", "CLAUDE_CONFIG_DIR"}

func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if slices.Contains(envPermitido, k) {
			env = append(env, kv)
		}
	}
	return env
}

func tail(s string) string { return tailN(s, 600) }

// tailN: los últimos n bytes de s, marcando el corte.
func tailN(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
