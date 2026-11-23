package fab

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func sinSrt(t *testing.T) {
	t.Helper()
	viejo := srtRuta
	t.Cleanup(func() { srtRuta = viejo })
	srtRuta = func() (string, error) { return "", exec.ErrNotFound }
}

// Sin srt (o sin perfil) no se ejecuta código de la rama en ningún camino, y el motivo es de infraestructura.
func TestSinSrtNoSeEjecutaNada(t *testing.T) {
	sinSrt(t)
	dir := t.TempDir()
	testigo := filepath.Join(dir, "corrio")
	linea := "touch " + testigo
	r := Repo{Name: "web", WT: dir, Check: linea, Encierro: filepath.Join(dir, "perfil.json")}
	ok, out := RunCheck(context.Background(), r)
	if ok || !strings.Contains(out, "npm install -g @anthropic-ai/sandbox-runtime") || !infraRe.MatchString(out) {
		t.Fatalf("chequeo sin srt: ok=%v %q", ok, out)
	}
	if _, err := arrancarApp(context.Background(), RecetaPreview{Comandos: []string{linea}}, dir, nil, filepath.Join(dir, "app.log"), r.Encierro); !errors.Is(err, errSinEncierro) {
		t.Fatalf("app sin srt: %v", err)
	}
	c := Config{Home: dir, Runs: filepath.Join(dir, "runs")}
	if err := os.MkdirAll(filepath.Join(c.Runs, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.prepararEncierro(context.Background(), "t1", filepath.Join(c.Runs, "t1"), []Repo{r}); err == nil || !infraRe.MatchString(err.Error()) {
		t.Fatalf("la sonda sin srt: %v", err)
	}
	if env := envChequeo(linea, r.Encierro); len(env) != 1 {
		t.Fatalf("sin srt el hook no recibe línea encerrada: %v", env)
	}
	if _, err := os.Stat(testigo); err == nil {
		t.Fatal("se ejecutó código de la rama sin encierro")
	}
	// Con srt pero sin perfil, tampoco.
	r.Encierro = ""
	srtRuta = func() (string, error) { return "/bin/true", nil }
	if ok, _ := RunCheck(context.Background(), r); ok {
		t.Fatal("sin perfil el chequeo no corre")
	}
}

// El argv es srt --settings <perfil> -c "bash -c '<línea>'", con la línea bien citada.
func TestArgvEncerrado(t *testing.T) {
	viejo := srtRuta
	t.Cleanup(func() { srtRuta = viejo })
	srtRuta = func() (string, error) { return "/opt/srt", nil }
	srt, args, err := argvEncerrado("/r/.cache/srt-t1.json", "echo 'hola' && make check")
	if err != nil || srt != "/opt/srt" {
		t.Fatal(srt, err)
	}
	quiero := []string{"--settings", "/r/.cache/srt-t1.json", "-c", "bash -c " + shQuote(conTmpDeTarea+"echo 'hola' && make check")}
	if !slices.Equal(args, quiero) {
		t.Fatalf("args %q", args)
	}
	linea, _ := lineaEncerrada("/r/p.json", "echo 'x'")
	// srt corre con el temporal de afuera (su socket); la línea, con el de la tarea.
	pre := "TMPDIR=" + shQuote(filepath.Clean(os.TempDir())) + " '/opt/srt' "
	if !strings.HasPrefix(linea, pre) {
		t.Fatalf("línea del hook: %s", linea)
	}
	out, err := exec.Command("bash", "-c", "printf '%s\\n' "+strings.TrimPrefix(linea, pre)).Output()
	_, args2, _ := argvEncerrado("/r/p.json", "echo 'x'")
	if err != nil || string(out) != strings.Join(args2, "\n")+"\n" {
		t.Fatalf("la línea del hook no se parte igual que el argv: %q %v", out, err)
	}
}

// El perfil: red sin GitHub, escritura en los worktrees, el clon y las cachés; ni hooks ni config de
// git, ni el estado de las tareas, ni fabrica.yaml; sin leer credenciales. Es JSON con el esquema de srt.
func TestPerfilEncierro(t *testing.T) {
	usuario := t.TempDir()
	antes := homeUsuario
	t.Cleanup(func() { homeUsuario = antes })
	homeUsuario = func() string { return usuario }
	home := t.TempDir()
	c := Config{Home: home, Factory: filepath.Join(home, "fab"), Runs: filepath.Join(home, "runs")}
	if err := os.MkdirAll(filepath.Join(c.Runs, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	clon := filepath.Join(home, "clones", "web")
	tarea := filepath.Join(c.Runs, "t1")
	wt := filepath.Join(tarea, "wt", "web")
	perfil, err := c.prepararEncierro(context.Background(), "t1", tarea, []Repo{{Name: "web", Src: clon, WT: wt}})
	if err != nil {
		t.Fatal(err)
	}
	type perfilSrt struct {
		Network struct {
			AllowedDomains []string `json:"allowedDomains"`
			DeniedDomains  []string `json:"deniedDomains"`
			Local          bool     `json:"allowLocalBinding"`
		} `json:"network"`
		Filesystem struct {
			AllowWrite, DenyWrite, DenyRead, AllowRead []string
		} `json:"filesystem"`
		AppleEvents *bool `json:"allowAppleEvents"`
		Trustd      bool  `json:"enableWeakerNetworkIsolation"`
	}
	leer := func(ruta string) (perfilSrt, string) {
		t.Helper()
		b, _ := os.ReadFile(ruta)
		var p perfilSrt
		if err := json.Unmarshal(b, &p); err != nil || p.Network.DeniedDomains == nil {
			t.Fatalf("perfil %s: %v", b, err)
		}
		return p, string(b)
	}
	p, b := leer(perfil)
	if strings.Contains(b, "github") || !p.Network.Local || !slices.Contains(p.Network.AllowedDomains, "registry.npmjs.org") ||
		slices.Contains(p.Network.AllowedDomains, "semgrep.dev") || slices.Contains(p.Network.AllowedDomains, "storage.googleapis.com") || p.Trustd {
		t.Fatalf("red: %+v", p.Network)
	}
	tmp, cache := dirsDeTarea(tarea)
	for _, w := range []string{filepath.Dir(wt), clon, tmp, cache} {
		if !slices.Contains(p.Filesystem.AllowWrite, w) {
			t.Errorf("no escribe en %s", w)
		}
	}
	// C4/H3: ni las cachés ni el temporal del usuario.
	for _, w := range []string{filepath.Join(usuario, "go/pkg/mod"), filepath.Join(usuario, ".npm"), filepath.Join(usuario, ".cache"), filepath.Clean(os.TempDir())} {
		if slices.Contains(p.Filesystem.AllowWrite, w) {
			t.Errorf("escribe en %s", w)
		}
	}
	for _, d := range []string{filepath.Join(clon, ".git", "hooks"), filepath.Join(clon, ".git", "config"), filepath.Join(clon, ".git", "commondir"),
		filepath.Join(wt, ".claude"), filepath.Join(wt, "CLAUDE.local.md"), c.dirAgentes(), "/tmp/claude",
		filepath.Join(c.Factory, "fabrica"), filepath.Join(home, "fabrica.yaml"), filepath.Join(c.Runs, "*", "*.json"), filepath.Join(c.Runs, ".cache")} {
		if !slices.Contains(p.Filesystem.DenyWrite, d) {
			t.Errorf("falta denyWrite %s", d)
		}
	}
	for _, d := range []string{filepath.Join(usuario, ".config", "gh"), filepath.Join(usuario, "Library", "Keychains"),
		filepath.Join(usuario, "Documents"), filepath.Join(usuario, ".claude"), filepath.Join(usuario, "Library", "Application Support")} {
		if !slices.Contains(p.Filesystem.DenyRead, d) {
			t.Errorf("falta denyRead %s: %v", d, p.Filesystem.DenyRead)
		}
	}
	if !slices.Contains(p.Filesystem.AllowRead, tarea) || !slices.Contains(p.Filesystem.AllowRead, clon) {
		t.Errorf("allowRead: %v", p.Filesystem.AllowRead)
	}
	if p.AppleEvents == nil || *p.AppleEvents {
		t.Errorf("allowAppleEvents tiene que estar en false explícito")
	}
	// El de herramientas: más red, trustd, y no escribe ni los worktrees ni los clones.
	h, hb := leer(perfilHerramientasDe(perfil))
	if !h.Trustd || !slices.Contains(h.Network.AllowedDomains, "semgrep.dev") || !slices.Contains(h.Network.AllowedDomains, "vuln.go.dev") ||
		strings.Contains(hb, "github") || h.Network.Local || slices.Contains(h.Filesystem.AllowWrite, clon) || slices.Contains(h.Filesystem.AllowWrite, filepath.Dir(wt)) {
		t.Errorf("perfil de herramientas: %s", hb)
	}
	for _, d := range []string{tmp, filepath.Join(cache, "semgrep")} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Errorf("no creó %s", d)
		}
	}
	if env := envChequeo("make check", perfil); len(env) != 2 || !strings.Contains(env[1], perfil) {
		t.Errorf("el hook no recibe la línea encerrada: %v", env)
	}
}

// El hook Stop solo ejecuta la línea encerrada: con FABRICA_CHECK suelto no corre nada.
func TestHookStopSoloEncerrado(t *testing.T) {
	hook, _ := filepath.Abs(filepath.Join("..", "..", "hooks", "check-on-stop.sh"))
	dir := t.TempDir()
	testigo := filepath.Join(dir, "corrio")
	correr := func(env ...string) error {
		cmd := exec.Command("bash", hook)
		cmd.Env, cmd.Stdin = append(cleanEnv(), env...), strings.NewReader(`{"stop_hook_active": false}`)
		return cmd.Run()
	}
	if err := correr("FABRICA_CHECK=touch " + testigo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(testigo); err == nil {
		t.Fatal("el hook corrió FABRICA_CHECK sin encierro")
	}
	if err := correr("FABRICA_CHECK_ENCERRADO=exit 3"); err == nil {
		t.Fatal("un chequeo rojo tiene que frenar el Stop")
	}
}
