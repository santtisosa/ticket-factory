package fab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Revisión de seguridad del 22/11 (C1–C4, H1–H3, M1–M4). Lo que necesita srt real se saltea sin srt.

func srtReal(t *testing.T) {
	t.Helper()
	p, err := exec.LookPath("srt")
	if err != nil {
		t.Skip("srt no está instalado")
	}
	viejo := srtRuta
	t.Cleanup(func() { srtRuta = viejo })
	srtRuta = func() (string, error) { return p, nil }
}

func conToken(t *testing.T, tok string) {
	t.Helper()
	viejo := tokenGitHub
	t.Cleanup(func() { tokenGitHub = viejo })
	tokenGitHub = tok
}

// C1: sin token propio, nada habla con GitHub; el token sale del entorno del proceso.
func TestTokenGitHub(t *testing.T) {
	conToken(t, "")
	if _, err := comandoGH(context.Background(), "pr", "list"); !errors.Is(err, errSinTokenGitHub) {
		t.Fatalf("gh sin token: %v", err)
	}
	if _, err := comandoGitGitHub(context.Background(), "", "ls-remote", "https://github.com/o/r.git"); !errors.Is(err, errSinTokenGitHub) {
		t.Fatalf("git sin token: %v", err)
	}
	if !strings.Contains(errSinTokenGitHub.Error(), "fine-grained") {
		t.Fatal("el error no dice qué hacer")
	}
	t.Setenv(VarTokenGitHub, "tok-de-prueba")
	guardarTokenGitHub()
	if os.Getenv(VarTokenGitHub) != "" || tokenGitHub != "tok-de-prueba" {
		t.Fatal("el token quedó en el entorno del proceso")
	}
	if !slices.Contains(EntornoConToken(), VarTokenGitHub+"=tok-de-prueba") {
		t.Fatal("relanzar la fábrica pierde el token")
	}
	cmd, err := comandoGH(context.Background(), "pr", "list")
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "GH_TOKEN=tok-de-prueba") || !strings.Contains(env, "GH_CONFIG_DIR=") || strings.Contains(env, VarTokenGitHub) {
		t.Fatalf("entorno de gh: %v", cmd.Env)
	}
	// Ni los agentes ni el chequeo lo heredan.
	for _, kv := range cleanEnv() {
		if strings.Contains(kv, "tok-de-prueba") {
			t.Fatal("cleanEnv lleva el token")
		}
	}
}

// C1: el git de GitHub usa solo el token: ni el helper de la config global del usuario, ni uno
// plantado en la config del repo (también con ámbito de URL), ni ssh.
func TestGitGitHubSoloConElToken(t *testing.T) {
	conToken(t, "tok-de-prueba")
	home := t.TempDir()
	t.Setenv("HOME", home)
	señuelo := "!f() { echo username=u; echo password=SENUELO; }; f"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[credential]\n\thelper = \""+señuelo+"\"\n[url \"git@github.com:\"]\n\tinsteadOf = https://github.com/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if out, err := exec.Command("git", "-C", repo, "config", "credential.https://github.com.helper", señuelo).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	for _, dir := range []string{"", repo} {
		cmd, err := comandoGitGitHub(context.Background(), dir, "credential", "fill")
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("credential fill: %v", err)
		}
		if strings.Count(string(out), "password=") != 1 || !strings.Contains(string(out), "password=tok-de-prueba") || strings.Contains(string(out), "SENUELO") {
			t.Fatalf("dir %q: %s", dir, out)
		}
	}
	if u, err := urlGitHub("git@github.com:o/r.git"); err != nil || u != "https://github.com/o/r.git" {
		t.Fatalf("url: %q %v", u, err)
	}
}

// C1: ningún exec de gh fuera de github.go (un camino nuevo usaría las credenciales del usuario).
func TestGHSoloPorGithubGo(t *testing.T) {
	re := regexp.MustCompile(`exec\.Command(Context)?\([^)]*"gh"`)
	fs, _ := filepath.Glob("*.go")
	for _, f := range fs {
		if strings.HasSuffix(f, "_test.go") || f == "github.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		if re.Match(b) {
			t.Errorf("%s ejecuta gh por su cuenta: use comandoGH", f)
		}
	}
	cmdFs, _ := filepath.Glob("../../cmd/*/*.go")
	for _, f := range cmdFs {
		b, _ := os.ReadFile(f)
		if !strings.HasSuffix(f, "_test.go") && re.Match(b) {
			t.Errorf("%s ejecuta gh por su cuenta", f)
		}
	}
}

// H1: un clon de la fábrica con .git/commondir plantado no se usa; uno sano lleva GIT_DIR y
// GIT_COMMON_DIR explícitos.
func TestClonConCommondirPlantado(t *testing.T) {
	_, r := worktreeDeTarea(t)
	env, err := entornoGit(r.Src)
	gd := filepath.Join(r.Src, ".git")
	if err != nil || !slices.Contains(env, "GIT_DIR="+gd) || !slices.Contains(env, "GIT_COMMON_DIR="+gd) {
		t.Fatalf("clon sano: %v %v", env, err)
	}
	if err := os.WriteFile(filepath.Join(gd, "commondir"), []byte("/otro/repo/.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(context.Background(), r.Src, "status"); !errors.Is(err, errWorktreeAjeno) {
		t.Fatalf("git sobre un clon con commondir: %v", err)
	}
	if _, err := entornoGitSeguro(r.Src); !errors.Is(err, errWorktreeAjeno) {
		t.Fatalf("entorno seguro: %v", err)
	}
}

// C3: el cwd de los agentes no está en un repo git ni tiene nada que Claude Code cargue; Run no arranca
// claude si no se cumple.
func TestCwdDeLosAgentes(t *testing.T) {
	limpio := t.TempDir()
	if err := cwdSeguro(limpio); err != nil {
		t.Fatalf("carpeta limpia: %v", err)
	}
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	adentro := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(adentro, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cwdSeguro(adentro); err == nil {
		t.Fatal("un cwd dentro de un repo git es inseguro")
	}
	plantado := t.TempDir()
	if err := os.MkdirAll(filepath.Join(plantado, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cwdSeguro(plantado); err == nil {
		t.Fatal("un .claude plantado en el cwd tiene que frenar")
	}
	if _, err := Run(context.Background(), Call{Dir: adentro}); !errors.Is(err, ErrInfra) {
		t.Fatalf("Run con cwd inseguro: %v", err)
	}
	if _, err := Run(context.Background(), Call{Dir: ""}); !errors.Is(err, ErrInfra) {
		t.Fatalf("Run sin cwd: %v", err)
	}
	c := Config{Home: "/espacio/uno"}
	if d := c.dirAgentes(); strings.HasPrefix(d, c.Home) || d == (Config{Home: "/espacio/dos"}).dirAgentes() {
		t.Fatalf("cwd de agentes: %s", d)
	}
	if got := carpetaDeProyecto("/Users/a/.openclaw/x_y"); got != "-Users-a--openclaw-x-y" {
		t.Fatalf("carpeta de proyecto: %s", got)
	}
}

// M3: de ~/Documents se niega todo menos el camino al espacio y a la fábrica.
func TestNegarHermanos(t *testing.T) {
	docs := t.TempDir()
	for _, d := range []string{"otro", "area/repos", "area/notas", "ticket-factory"} {
		if err := os.MkdirAll(filepath.Join(docs, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(docs, "notas.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := negarHermanos(docs, []string{filepath.Join(docs, "area", "repos"), filepath.Join(docs, "ticket-factory")})
	quiero := []string{filepath.Join(docs, "area", "notas"), filepath.Join(docs, "notas.txt"), filepath.Join(docs, "otro")}
	slices.Sort(got)
	if !slices.Equal(got, quiero) {
		t.Fatalf("negados %v, quería %v", got, quiero)
	}
}

// H2: la base se fija al crear la tarea y no la mueve el ref origin/main del clon; avanza solo a un main
// que trajo la fábrica y que la rama contiene.
func TestBaseFija(t *testing.T) {
	ctx := context.Background()
	origin, r := repoConRama(t)
	tk := &Task{Dir: t.TempDir()}
	repos := []Repo{r}
	if err := cargarBase(ctx, tk, repos); err != nil {
		t.Fatal(err)
	}
	inicial := repos[0].BaseSHA
	if inicial == "" || baseGuardada(tk, r.Name) != inicial || baseDe(repos[0]) != inicial {
		t.Fatalf("base %q", inicial)
	}
	escribir(t, r.WT, "c.txt", "tarea\n")
	sh(t, r.WT, "add", ".")
	sh(t, r.WT, "commit", "-q", "-m", "tarea")
	// El Builder mueve origin/main a su HEAD: la base no cambia.
	head := strings.TrimSpace(sh(t, r.WT, "rev-parse", "HEAD"))
	sh(t, r.Src, "update-ref", "refs/remotes/origin/main", head)
	repos2 := []Repo{r}
	if err := cargarBase(ctx, tk, repos2); err != nil || repos2[0].BaseSHA != inicial {
		t.Fatalf("la base siguió al ref: %q %v", repos2[0].BaseSHA, err)
	}
	// Main avanzó en GitHub; la fábrica lo trae. Antes de rebasar, la base no avanza; después, sí.
	escribir(t, origin, "b.txt", "otra PR\n")
	sh(t, origin, "add", ".")
	sh(t, origin, "commit", "-q", "-m", "otra PR")
	sh(t, r.Src, "fetch", "-q", "origin", "+main:refs/remotes/origin/main")
	nuevo, err := mainTraido(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := avanzarBase(ctx, tk, repos2, map[string]string{r.Name: nuevo}); err != nil || repos2[0].BaseSHA != inicial {
		t.Fatalf("avanzó sin que la rama tenga ese main: %q %v", repos2[0].BaseSHA, err)
	}
	if _, err := alDia(ctx, repos2[0], nuevo); !errors.Is(err, errRebaso) {
		t.Fatalf("rebase: %v", err)
	}
	if err := avanzarBase(ctx, tk, repos2, map[string]string{r.Name: nuevo}); err != nil || repos2[0].BaseSHA != nuevo || baseGuardada(tk, r.Name) != nuevo {
		t.Fatalf("no avanzó tras el rebase: %q %v", repos2[0].BaseSHA, err)
	}
	if !strings.Contains(SemgrepCmdCon(repos2[0], "/bin/semgrep"), "--baseline-commit "+nuevo) {
		t.Fatal("semgrep no usa la base fija")
	}
}

// H2: los archivos que cambian cómo se construye, se prueba o se despliega no se integran solos.
func TestSensiblesNuevos(t *testing.T) {
	for _, f := range []string{"cloudbuild.yaml", "infra/cloudbuild-prod.yml", "web/next.config.js", "vite.config.ts",
		"jest.config.mjs", "x/eslint.config.cjs", ".gitattributes", ".gitmodules", "scripts/deploy.sh", "web/scripts/x.js",
		".claude/settings.local.json", "web/.claude/hooks.json"} {
		if patronSensible(f, nil) == "" {
			t.Errorf("%s no es sensible", f)
		}
	}
	if p := patronSensible("web/src/config.ts", nil); p != "" {
		t.Errorf("config.ts común marcado por %s", p)
	}
}

// C2: UX usa una lista cerrada de herramientas de Playwright, sin ejecutar código ni subir archivos,
// y su frontmatter dice lo mismo.
func TestHerramientasUX(t *testing.T) {
	ts := herramientasUX()
	if slices.Contains(ts, "mcp__playwright") {
		t.Fatal("Playwright entero")
	}
	for _, n := range playwrightNegadas {
		if slices.Contains(ts, "mcp__playwright__"+n) {
			t.Errorf("permitida: %s", n)
		}
	}
	for _, n := range []string{"browser_navigate", "browser_snapshot", "browser_take_screenshot"} {
		if !slices.Contains(ts, "mcp__playwright__"+n) {
			t.Errorf("falta %s", n)
		}
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "agents", "ux.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^tools: (.*)$`).FindSubmatch(b)
	var fm []string
	for _, x := range strings.Split(string(m[1]), ",") {
		if x = strings.TrimSpace(x); strings.HasPrefix(x, "mcp__playwright__") {
			fm = append(fm, strings.TrimPrefix(x, "mcp__playwright__"))
		}
	}
	if !slices.Equal(fm, playwrightPermitidas) {
		t.Fatalf("ux.md dice %v; la llamada usa %v", fm, playwrightPermitidas)
	}
}

// C2: el hook niega cualquier herramienta de Playwright con nombre de archivo propio.
func TestHookPlaywrightSinArchivos(t *testing.T) {
	hook, _ := filepath.Abs(filepath.Join("..", "..", "hooks", "playwright-sin-archivos.sh"))
	for entrada, quiere := range map[string]int{
		`{"tool_name":"mcp__playwright__browser_take_screenshot","tool_input":{"filename":"../.claude/settings.local.json"}}`: 2,
		`{"tool_name":"mcp__playwright__browser_console_messages","tool_input":{"level":"info","filename":"x"}}`:              2,
		`{"tool_name":"mcp__playwright__browser_file_upload","tool_input":{"paths":["/etc/hosts"]}}`:                          2,
		`{"tool_name":"mcp__playwright__browser_navigate","tool_input":{"url":"http://127.0.0.1:3000"}}`:                      0,
		`no es json`: 2,
	} {
		cmd := exec.Command("bash", hook)
		cmd.Stdin = strings.NewReader(entrada)
		err := cmd.Run()
		got := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			got = ee.ExitCode()
		}
		if got != quiere {
			t.Errorf("%s: salida %d, quería %d", entrada, got, quiere)
		}
	}
}

// LOW: la sonda negativa frena un "srt" que no aísla (el falso de TestMain deja escribir el home).
func TestSondaNegativa(t *testing.T) {
	viejo := sonda
	t.Cleanup(func() { sonda = viejo })
	sonda = sondaNegativa
	home := t.TempDir()
	c := Config{Home: home, Factory: filepath.Join(home, "fab"), Runs: filepath.Join(home, "runs")}
	if err := os.MkdirAll(filepath.Join(c.Runs, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := c.prepararEncierro(context.Background(), "t1", filepath.Join(c.Runs, "t1"), nil)
	if err == nil || !strings.Contains(err.Error(), "no aísla") || !infraRe.MatchString(err.Error()) {
		t.Fatalf("un srt que no aísla pasó la sonda: %v", err)
	}
}

// Con srt real: la sonda pasa, el chequeo escribe el worktree y su temporal, y no lee ~/Documents, no
// escribe el commondir del clon ni el .claude del worktree, y no manda Apple Events.
func TestEncierroReal(t *testing.T) {
	srtReal(t)
	viejo := sonda
	t.Cleanup(func() { sonda = viejo })
	sonda = sondaNegativa
	home, r := worktreeDeTarea(t)
	c := Config{Home: home, Factory: filepath.Join(home, "fab"), Runs: filepath.Join(home, "runs")}
	if err := os.MkdirAll(filepath.Join(c.Runs, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Un .claude/ que ya trae la rama: tampoco se escribe adentro.
	escribir(t, r.WT, ".claude/settings.json", "{}\n")
	perfil, err := c.prepararEncierro(context.Background(), "t1", dirTareaDe(r.WT), []Repo{r})
	if err != nil {
		t.Fatal(err)
	}
	linea := `echo x > nuevo.txt && echo WT_OK
echo '{"hooks":{}}' > .claude/settings.local.json 2>/dev/null && echo ESCRIBE_CLAUDE_EXISTENTE
echo x > CLAUDE.local.md 2>/dev/null && echo ESCRIBE_CLAUDE_LOCAL
mktemp "$TMPDIR/x.XXXXXX" >/dev/null && echo TMP_OK
ls "$HOME/Documents" >/dev/null 2>&1 && echo LEE_DOCUMENTS
echo x > ` + shQuote(filepath.Join(r.Src, ".git", "commondir")) + ` 2>/dev/null && echo ESCRIBE_COMMONDIR
mkdir -p .claude 2>/dev/null && echo x > .claude/settings.local.json 2>/dev/null && echo ESCRIBE_CLAUDE
osascript -e 'tell application "System Events" to get name of first process' >/dev/null 2>&1 && echo APPLE_EVENTS
true`
	cmd, err := comandoEncerrado(context.Background(), perfil, linea)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Dir, cmd.Env = r.WT, append(cleanEnv(), entornoDeTarea(dirTareaDe(r.WT))...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	s := string(out)
	for _, quiero := range []string{"WT_OK", "TMP_OK"} {
		if !strings.Contains(s, quiero) {
			t.Errorf("falta %s: %s", quiero, s)
		}
	}
	for _, no := range []string{"ESCRIBE_COMMONDIR", "ESCRIBE_CLAUDE", "ESCRIBE_CLAUDE_EXISTENTE", "ESCRIBE_CLAUDE_LOCAL", "APPLE_EVENTS"} {
		if strings.Contains(s, no) {
			t.Errorf("el encierro dejó pasar %s", no)
		}
	}
	if _, err := os.Stat(filepath.Join(homeUsuario(), "Documents")); err == nil && strings.Contains(s, "LEE_DOCUMENTS") &&
		!dentroDe(home, filepath.Join(homeUsuario(), "Documents")) {
		t.Error("el encierro lee ~/Documents")
	}
}

// LOW: gitleaks corre en srt con la config de la fábrica: el allowlist de la rama no esconde un secreto.
func TestGitleaksIgnoraLaConfigDeLaRama(t *testing.T) {
	srtReal(t)
	if _, ok := buscarBinario("gitleaks"); !ok {
		t.Skip("gitleaks no está instalado")
	}
	home, r := worktreeDeTarea(t)
	c := Config{Home: home, Factory: filepath.Join(home, "fab"), Runs: filepath.Join(home, "runs")}
	if err := os.MkdirAll(filepath.Join(c.Runs, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(sh(t, r.WT, "rev-parse", "HEAD"))
	escribir(t, r.WT, "s.go", "package a\nconst AWS = \"AKIAIOSFODNN7EXAMPLQ\" // gitleaks:allow\n")
	escribir(t, r.WT, ".gitleaks.toml", "[allowlist]\npaths = [\"s.go\"]\n")
	escribir(t, r.WT, ".gitleaksignore", "s.go:aws-access-token:2\n")
	sh(t, r.WT, "add", ".")
	sh(t, r.WT, "commit", "-q", "-m", "secreto")
	perfil, err := c.prepararEncierro(context.Background(), "t1", dirTareaDe(r.WT), []Repo{r})
	if err != nil {
		t.Fatal(err)
	}
	r.Encierro, r.BaseSHA = perfil, base
	var gl *Review
	for _, rv := range c.escaneo(context.Background(), []Repo{r}) {
		if rv.Revisor == revisorHerramienta+"gitleaks" {
			gl = &rv
		}
	}
	if gl == nil || gl.Err != "" || len(gl.Hallazgos) == 0 {
		b, _ := json.Marshal(gl)
		t.Fatalf("gitleaks no vio el secreto: %s", b)
	}
	if bytes.Contains([]byte(gl.Hallazgos[0].Evidencia), []byte("AKIA")) {
		t.Fatal("el secreto viajó al hallazgo")
	}
}

// C4/M2: gosec y govulncheck corren en srt con el perfil de herramientas y de verdad revisan (un
// exec.Command con una línea variable es G204); si no cargaran paquetes, quedarían "sin revisar".
// (El G201 de ventaMala no lo marca esta versión de gosec ni fuera de srt.)
const codigoG204 = "package x\n\nimport \"os/exec\"\n\nfunc R(c string) error { return exec.Command(\"sh\", \"-c\", c).Run() }\n"

func TestEscaneoRealGo(t *testing.T) {
	srtReal(t)
	if _, ok := buscarBinario("gosec"); !ok {
		t.Skip("gosec no está instalado")
	}
	home, r := worktreeDeTarea(t)
	c := Config{Home: home, Factory: filepath.Join(home, "fab"), Runs: filepath.Join(home, "runs")}
	if err := os.MkdirAll(filepath.Join(c.Runs, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(sh(t, r.WT, "rev-parse", "HEAD"))
	escribir(t, r.WT, "go.mod", "module x\n\ngo 1.22\n")
	escribir(t, r.WT, "r.go", codigoG204)
	sh(t, r.WT, "add", ".")
	sh(t, r.WT, "commit", "-q", "-m", "r")
	perfil, err := c.prepararEncierro(context.Background(), "t1", dirTareaDe(r.WT), []Repo{r})
	if err != nil {
		t.Fatal(err)
	}
	r.Encierro, r.BaseSHA = perfil, base
	revs := map[string]Review{}
	for _, rv := range c.escaneo(context.Background(), []Repo{r}) {
		revs[strings.TrimPrefix(rv.Revisor, revisorHerramienta)] = rv
	}
	g := revs["gosec"]
	if g.Err != "" || !slices.ContainsFunc(g.Hallazgos, func(h Hallazgo) bool { return h.Regla == "gosec-G204" }) {
		b, _ := json.Marshal(g)
		t.Fatalf("gosec en srt: %s", b)
	}
	// govulncheck depende de vuln.go.dev: un corte de red lo deja "sin revisar", que es la conducta esperada.
	if v, ok := revs["govulncheck"]; ok && v.Err != "" {
		t.Logf("govulncheck en srt quedó sin revisar: %s", v.Err)
	}
}
