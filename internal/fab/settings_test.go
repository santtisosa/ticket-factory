package fab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Revisiones de seguridad del 10/11 y del 22/11: nadie lee credenciales ni el llavero; el Builder escribe
// solo en los clones de su tarea, su temporal y sus cachés; nadie sale a storage.googleapis.com; nadie
// edita el binario, los hooks, la config ni el commondir de los clones, ni lo que Claude Code carga de un
// directorio.
func TestSettingsEncierro(t *testing.T) {
	home := t.TempDir()
	usuario := t.TempDir() // el home de la cuenta, distinto del espacio de trabajo
	antes := homeUsuario
	t.Cleanup(func() { homeUsuario = antes })
	homeUsuario = func() string { return usuario }
	c := Config{Home: home, Factory: filepath.Join(home, "fabrica"), Runs: filepath.Join(home, "runs")}
	tarea := filepath.Join(c.Runs, "t1")
	repos := []Repo{{Name: "web", Src: filepath.Join(home, "clones", "web"), WT: filepath.Join(tarea, "wt", "web")}}
	leer := func(kind string) map[string]any {
		t.Helper()
		p := filepath.Join(t.TempDir(), kind+".json")
		if kind == "builder" || kind == "resultado" || kind == "lector" {
			var err error
			if p, err = c.settingsDeTarea(tarea, kind, repos); err != nil {
				t.Fatal(err)
			}
		} else if err := c.writeSettings(p, kind); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(p)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	gcloud := filepath.Join(usuario, ".config", "gcloud")
	if strings.HasPrefix(gcloud, home) {
		t.Fatal("el test necesita homes distintos")
	}
	for _, kind := range []string{"", "builder", "resultado", "biblio"} {
		s := leer(kind)
		deny, _ := json.Marshal(s["permissions"])
		for _, quiero := range []string{"Read(/" + gcloud + "/**)", "Edit(/" + filepath.Join(c.Factory, "fabrica") + ")", "/.git/hooks/**",
			"Edit(/" + filepath.Join(home, "fabrica.yaml") + ")", "Edit(/" + filepath.Join(c.Runs, ".cache") + "/**)",
			"Read(/" + filepath.Join(usuario, "Library", "Keychains") + "/**)", "Edit(/" + filepath.Join(home, "clones") + "/*/.git/commondir)",
			"/wt/*/.claude/**)", "/wt/*/CLAUDE.local.md)", "Edit(/" + c.dirAgentes() + "/**)",
			"mcp__playwright__browser_evaluate", "mcp__playwright__browser_run_code_unsafe"} {
			if !strings.Contains(string(deny), quiero) {
				t.Errorf("%q: falta %s en deny", kind, quiero)
			}
		}
		if hooks, _ := json.Marshal(s["hooks"]); !strings.Contains(string(hooks), "mcp__playwright__.*") || !strings.Contains(string(hooks), "playwright-sin-archivos.sh") {
			t.Errorf("%q: falta el hook que impide a Playwright escribir archivos con nombre: %s", kind, hooks)
		}
		// El .git entero de los clones, menos para el Builder: Claude Code suma el deny a su sandbox y
		// su git commit escribe ahí.
		git := "Edit(/" + filepath.Join(home, "clones") + "/*/.git/**)"
		if got := strings.Contains(string(deny), git); got != (kind != "builder") {
			t.Errorf("%q: deny de %s = %v", kind, git, got)
		}
	}
	sandbox := func(kind string) string {
		b, _ := json.Marshal(leer(kind)["sandbox"])
		return string(b)
	}
	b, r := sandbox("builder"), sandbox("resultado")
	if !strings.Contains(b, `"`+repos[0].Src+`"`) || strings.Contains(b, `"`+filepath.Join(c.Home, "clones")+`"`) {
		t.Fatalf("el Builder escribe en los clones de su tarea y en ningún otro: %s", b)
	}
	escribe := func(kind string) string {
		b, _ := json.Marshal(leer(kind)["sandbox"].(map[string]any)["filesystem"].(map[string]any)["allowWrite"])
		return string(b)
	}
	if strings.Contains(escribe("resultado"), repos[0].Src) {
		t.Fatalf("el revisor del resultado no escribe en los clones: %s", r)
	}
	tmp, cache := dirsDeTarea(tarea)
	for _, s := range []string{b, r} {
		if !strings.Contains(s, `"denyRead"`) || !strings.Contains(s, gcloud) || !strings.Contains(s, filepath.Join(usuario, "Documents")) ||
			!strings.Contains(s, filepath.Join(usuario, "Library", "Keychains")) {
			t.Fatalf("el sandbox no niega leer credenciales, el llavero o ~/Documents: %s", s)
		}
		if strings.Contains(s, "storage.googleapis.com") || strings.Contains(s, filepath.Join(usuario, "go", "pkg", "mod")) ||
			!strings.Contains(s, tmp) || !strings.Contains(s, cache) || !strings.Contains(s, c.dirAgentes()) {
			t.Fatalf("sandbox: cachés y temporal de la tarea, cwd de los agentes negado, sin el bucket: %s", s)
		}
	}
}

// El MCP de Playwright corre afuera: caché de npm propia y cwd fijo bajo runs/.cache, que el encierro
// y los agentes no escriben; nunca ~/.npm ni el worktree.
func TestMCPPlaywrightCachePropia(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "runs", ".cache")
	b, err := mcpPlaywright(cache, "http://127.0.0.1:4321", "/r/t1/ui-v1/playwright")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		McpServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	p := m.McpServers["playwright"]
	dir := filepath.Join(cache, "npm-fabrica")
	if p.Env["npm_config_cache"] != dir || len(p.Args) != 2 || !strings.HasPrefix(p.Args[1], "cd '"+dir+"' && exec npx -y @playwright/mcp@"+versionPlaywrightMCP) ||
		!strings.Contains(p.Args[1], "--allowed-origins 'http://127.0.0.1:4321'") || !strings.Contains(p.Args[1], "--output-dir '/r/t1/ui-v1/playwright'") {
		t.Fatalf("mcp: %s", b)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatal("no creó la caché propia")
	}
	// Y esa carpeta está negada al encierro y a los agentes (por runs/.cache).
	c := Config{Home: t.TempDir(), Runs: filepath.Dir(cache)}
	perfil, _ := json.Marshal(c.perfilEncierro(filepath.Join(c.Runs, "t1"), nil, false))
	if !strings.Contains(string(perfil), `"`+cache+`"`) {
		t.Fatalf("el encierro escribe runs/.cache: %s", perfil)
	}
}
