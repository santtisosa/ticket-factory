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

// Revisión de seguridad del 23/11: reemplazos de git, lectores sin sandbox, Edit libre del Builder,
// tareas que comparten clon y el commit aprobado leído tarde.

func gitCrudoT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=x", "-c", "user.email=x@x"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitConArchivo commitea f con ese contenido en el worktree y devuelve el sha.
func commitConArchivo(t *testing.T, wt, f, contenido string) string {
	t.Helper()
	escribir(t, wt, f, contenido)
	gitCrudoT(t, wt, "add", f)
	gitCrudoT(t, wt, "commit", "-qm", "c "+contenido)
	return gitCrudoT(t, wt, "rev-parse", "HEAD")
}

// worktreeDeOtraTarea: un worktree de la tarea t2 en el mismo clon que el de worktreeDeTarea.
func worktreeDeOtraTarea(t *testing.T, home string, r Repo) Repo {
	t.Helper()
	otra := Repo{Name: r.Name, Src: r.Src, WT: filepath.Join(home, "runs", "t2", "wt", "web")}
	gitCrudoT(t, r.Src, "worktree", "add", "-q", "-b", "factory/t2", otra.WT)
	return otra
}

// 1. CRITICAL: con `git replace X Y` todo lo que mostraba git era Y y el push mandaba X. La fábrica ve X,
// y guardar o aprobar con reemplazos, grafts, alternates o atributos plantados falla.
func TestReemplazosNoEnganan(t *testing.T) {
	_, r := worktreeDeTarea(t)
	ctx := context.Background()
	benigno := commitConArchivo(t, r.WT, "f.txt", "benigno\n")
	gitCrudoT(t, r.WT, "reset", "-q", "--hard", "HEAD~1")
	malicioso := commitConArchivo(t, r.WT, "f.txt", "MALICIOSO\n")
	if err := verificarAprobado(ctx, r.Src, r.WT, "factory/t1", malicioso); err != nil {
		t.Fatalf("sin nada plantado, lo aprobado pasa: %v", err)
	}
	gitCrudoT(t, r.Src, "replace", malicioso, benigno)
	if got := gitCrudoT(t, r.WT, "show", "HEAD:f.txt"); got != "benigno" {
		t.Fatalf("el git común tendría que ver el reemplazo (si no, el test no prueba nada): %q", got)
	}
	if got, err := git(ctx, r.WT, "show", "HEAD:f.txt"); err != nil || got != "MALICIOSO" {
		t.Fatalf("la fábrica ve lo que se sube, no el reemplazo: %q %v", got, err)
	}
	if err := verificarAprobado(ctx, r.Src, r.WT, "factory/t1", malicioso); !errors.Is(err, errGitAlterado) {
		t.Fatalf("con un ref de reemplazo no se aprueba: %v", err)
	}
	gitCrudoT(t, r.Src, "replace", "-d", malicioso)
	gd := filepath.Join(r.Src, ".git")
	for _, p := range archivosQueEnganan {
		escribir(t, gd, p, malicioso+"\n")
		if err := integridadGit(ctx, r.Src); !errors.Is(err, errGitAlterado) {
			t.Errorf("con .git/%s plantado no se aprueba: %v", p, err)
		}
		if err := os.Remove(filepath.Join(gd, p)); err != nil {
			t.Fatal(err)
		}
	}
	// grafts: GIT_NO_REPLACE_OBJECTS no los cubre (probado); GIT_GRAFT_FILE sí.
	escribir(t, gd, "info/grafts", malicioso+"\n")
	if out, err := git(ctx, r.WT, "rev-list", "--count", "HEAD"); err != nil || out != "2" {
		t.Fatalf("la fábrica ve la historia real, sin grafts: %q %v", out, err)
	}
}

// 1 y 3: el git de la fábrica no sigue la config global (diff.external) y el de los lectores no sigue
// los .gitattributes de la rama (un `* -diff` le mostraba "Binary files differ" al revisor).
func TestGitSinConfigGlobalNiAtributosDeLaRama(t *testing.T) {
	_, r := worktreeDeTarea(t)
	ctx := context.Background()
	falso := t.TempDir()
	marca := filepath.Join(falso, "corrio")
	escribir(t, falso, "ext.sh", "#!/bin/sh\ntouch "+marca+"\n")
	if err := os.Chmod(filepath.Join(falso, "ext.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	escribir(t, falso, ".gitconfig", "[diff]\n\texternal = "+filepath.Join(falso, "ext.sh")+"\n")
	t.Setenv("HOME", falso)
	escribir(t, r.WT, ".gitattributes", "* -diff\n")
	commitConArchivo(t, r.WT, "secreto.txt", "clave=1234\n")
	if _, err := git(ctx, r.WT, "diff", "HEAD~1", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marca); err == nil {
		t.Fatal("el git de la fábrica corrió el diff.external de la config global")
	}
	cmd := exec.Command("git", "-C", r.WT, "diff", "HEAD~1", "HEAD")
	cmd.Env = conEntorno(os.Environ(), entornoGitLector())
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "+clave=1234") {
		t.Fatalf("el lector ve el texto aunque la rama diga -diff: %v %s", err, out)
	}
	for _, kv := range []string{"GIT_NO_REPLACE_OBJECTS=1", "GIT_GRAFT_FILE=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1"} {
		if !slices.Contains(entornoBlindaje(), kv) {
			t.Errorf("falta %s en el blindaje", kv)
		}
	}
	if !slices.Contains(entornoGitBuilder(), "GIT_NO_REPLACE_OBJECTS=1") {
		t.Error("el Builder ve los reemplazos")
	}
}

// 1 y 4, con srt real: el código de la rama commitea y rebasa, pero no planta reemplazos, atributos,
// alternates ni refs empaquetadas, ni toca la rama o el gitdir de otra tarea del mismo clon.
func TestSrtNiegaReemplazosYRastrosDeOtras(t *testing.T) {
	srtReal(t)
	home, r := worktreeDeTarea(t)
	otra := worktreeDeOtraTarea(t, home, r)
	c := Config{Home: home, Factory: filepath.Join(home, "fab"), Runs: filepath.Join(home, "runs")}
	if err := os.MkdirAll(filepath.Join(c.Runs, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	perfil, err := c.prepararEncierro(context.Background(), "t1", dirTareaDe(r.WT), []Repo{r})
	if err != nil {
		t.Fatal(err)
	}
	gdOtra := gitdirDe(t, otra)
	gd := filepath.Join(r.Src, ".git")
	g := "git -c user.name=x -c user.email=x@x "
	linea := `echo x > nuevo.txt && ` + g + `add nuevo.txt && ` + g + `commit -qm n && echo COMMIT_OK
` + g + `replace HEAD HEAD~1 2>/dev/null && echo REPLACE
echo '* -diff' > ` + shQuote(filepath.Join(gd, "info", "attributes")) + ` 2>/dev/null && echo ATTRIBUTES
echo /x > ` + shQuote(filepath.Join(gd, "objects", "info", "alternates")) + ` 2>/dev/null && echo ALTERNATES
` + g + `pack-refs --all 2>/dev/null && echo PACKED
` + g + `update-ref refs/heads/factory/t2 HEAD 2>/dev/null && echo REF_OTRA
echo x > ` + shQuote(filepath.Join(gdOtra, "HEAD")) + ` 2>/dev/null && echo GITDIR_OTRA
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
	if !strings.Contains(s, "COMMIT_OK") {
		t.Fatalf("el código de la rama tiene que poder commitear: %s", s)
	}
	for _, no := range []string{"REPLACE", "ATTRIBUTES", "ALTERNATES", "PACKED", "REF_OTRA", "GITDIR_OTRA"} {
		if strings.Contains(s, no) {
			t.Errorf("el encierro dejó pasar %s", no)
		}
	}
	if err := integridadGit(context.Background(), r.Src); err != nil {
		t.Errorf("quedó algo plantado: %v", err)
	}
}

// 4: el perfil y el sandbox de una tarea niegan el gitdir y la rama de las otras del mismo clon, aunque
// la ref esté empaquetada, y no los propios.
func TestRastrosDeOtrasTareas(t *testing.T) {
	home, r := worktreeDeTarea(t)
	otra := worktreeDeOtraTarea(t, home, r)
	gitCrudoT(t, r.Src, "pack-refs", "--all")
	ps := rastrosDeOtrasTareas(r.Src, dirTareaDe(r.WT))
	propio, ajeno := gitdirDe(t, r), gitdirDe(t, otra)
	quiero := []string{ajeno, filepath.Join(r.Src, ".git", "refs", "heads", "factory", "t2")}
	for _, q := range quiero {
		if !slices.ContainsFunc(ps, func(p string) bool { return mismaRuta(p, q) || p == q }) {
			t.Errorf("falta negar %s: %v", q, ps)
		}
	}
	for _, p := range ps {
		if mismaRuta(p, propio) || strings.HasSuffix(p, "factory/t1") {
			t.Errorf("niega lo propio: %s", p)
		}
	}
	c := Config{Home: home, Factory: filepath.Join(home, "fab"), Runs: filepath.Join(home, "runs")}
	b, _ := json.Marshal(c.sandboxDeTarea(dirTareaDe(r.WT), []Repo{r}, "builder", nil))
	if !strings.Contains(string(b), "factory/t2") {
		t.Errorf("el sandbox del Builder no niega la rama de la otra tarea: %s", b)
	}
}

// 2 y 3: los lectores tienen sandbox sin red ni escritura en lo que leen, sin los comandos que ejecutan
// o escriben; el Builder edita solo sus worktrees; nadie escribe memoria automática.
func TestSettingsLectoresYEditDelBuilder(t *testing.T) {
	home, r := worktreeDeTarea(t)
	c := Config{Home: home, Factory: filepath.Join(home, "fab"), Runs: filepath.Join(home, "runs")}
	tarea := dirTareaDe(r.WT)
	origen := filepath.Join(home, "repos", "web")
	leer := func(kind string, sl ...string) map[string]any {
		t.Helper()
		p, err := c.settingsDeTarea(tarea, kind, []Repo{r}, sl...)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(p)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	for _, kind := range []string{"lector", "spec"} {
		m := leer(kind, r.WT, origen)
		sb := m["sandbox"].(map[string]any)
		if sb["enabled"] != true || sb["failIfUnavailable"] != true || sb["autoAllowBashIfSandboxed"] != false || sb["allowUnsandboxedCommands"] != false {
			t.Errorf("%s: sandbox obligatorio y sin autoAllow (si no, la lista de comandos no cuenta): %v", kind, sb)
		}
		red, _ := json.Marshal(sb["network"])
		if !strings.Contains(string(red), `"allowedDomains":[]`) || !strings.Contains(string(red), `"allowLocalBinding":false`) {
			t.Errorf("%s: sin red: %s", kind, red)
		}
		fs := sb["filesystem"].(map[string]any)
		niega, _ := json.Marshal(fs["denyWrite"])
		for _, p := range []string{r.WT, r.Src, origen} {
			if !strings.Contains(string(niega), `"`+p+`"`) {
				t.Errorf("%s: escribe en %s: %s", kind, p, niega)
			}
		}
		if lee, _ := json.Marshal(fs["allowRead"]); !strings.Contains(string(lee), origen) {
			t.Errorf("%s: no lee su --add-dir: %s", kind, lee)
		}
		deny, _ := json.Marshal(m["permissions"].(map[string]any)["deny"])
		for _, d := range bashNegadoLectores {
			if !strings.Contains(string(deny), d) {
				t.Errorf("%s: falta negar %s", kind, d)
			}
		}
		_, excluye := sb["excludedCommands"]
		if excluye != (kind == "spec") {
			t.Errorf("%s: excludedCommands = %v", kind, sb["excludedCommands"])
		}
	}
	for _, peligroso := range []string{"Bash(git grep:*)", "Bash(rg:*)", "Bash(sed -n:*)", "Bash(sort:*)", "Bash(uniq:*)"} {
		if slices.Contains(readOnlyTools, peligroso) {
			t.Errorf("los lectores todavía tienen %s", peligroso)
		}
	}
	if slices.Contains(herramientasBuilder, "Edit") || slices.Contains(herramientasBuilder, "Write") {
		t.Fatal("un Edit o Write suelto deja al Builder escribir en cualquier lado")
	}
	b := leer("builder")
	allow, _ := b["permissions"].(map[string]any)["allow"].([]any)
	if len(allow) == 0 {
		t.Fatal("el Builder no puede editar sus worktrees")
	}
	for _, a := range allow {
		s := a.(string)
		if !strings.HasPrefix(s, "Edit(//") || !strings.HasSuffix(s, "/**)") || !strings.Contains(s, filepath.Join("runs", "t1", "wt", "web")) {
			t.Errorf("allow de Edit fuera del worktree de la tarea: %s", s)
		}
	}
	for _, kind := range []string{"builder", "lector", "resultado"} {
		if leer(kind)["autoMemoryEnabled"] != false {
			t.Errorf("%s: memoria automática encendida", kind)
		}
	}
}

// 5: lo aprobado es el commit fijado antes de las revisiones; si la rama se movió (aunque vuelva), si hay
// cambios sueltos o reemplazos plantados, la vuelta no aprueba.
func TestGuardarAprobadoExigeElCommitFijado(t *testing.T) {
	_, r := worktreeDeTarea(t)
	ctx := context.Background()
	commitConArchivo(t, r.WT, "f.txt", "uno\n")
	fijos, revisados, err := fijarRevisados(ctx, []Repo{r})
	if err != nil {
		t.Fatal(err)
	}
	if fijos[0].HeadSHA == "" || r.HeadSHA != "" {
		t.Fatal("fijarRevisados devuelve una copia con el commit fijado")
	}
	tk := &Task{Dir: t.TempDir(), Est: Estado{ID: "t1"}}
	if err := guardarAprobado(ctx, tk, []Repo{r}, revisados); err != nil {
		t.Fatalf("sin cambios, se aprueba lo fijado: %v", err)
	}
	var guardado map[string]string
	if err := tk.ReadJSON("aprobado.json", &guardado); err != nil || guardado["web"] != revisados["web"] {
		t.Fatalf("aprobado.json = %v %v", guardado, err)
	}
	if err := guardarAprobado(ctx, tk, []Repo{r}, nil); err == nil {
		t.Fatal("sin commit fijado no se aprueba")
	}
	commitConArchivo(t, r.WT, "f.txt", "dos\n")
	if err := guardarAprobado(ctx, tk, []Repo{r}, revisados); err == nil {
		t.Fatal("un commit después de fijar (la app de UX, un proceso suelto) no se aprueba")
	}
	gitCrudoT(t, r.WT, "reset", "-q", "--hard", revisados["web"])
	gitCrudoT(t, r.Src, "replace", revisados["web"], revisados["web"]+"~1")
	if err := guardarAprobado(ctx, tk, []Repo{r}, revisados); !errors.Is(err, errGitAlterado) {
		t.Fatalf("con un reemplazo plantado no se aprueba: %v", err)
	}
	gitCrudoT(t, r.Src, "replace", "-d", revisados["web"])
	// La rama en su lugar pero el worktree en otro commit: tampoco.
	gitCrudoT(t, r.WT, "checkout", "-q", "--detach", revisados["web"]+"~1")
	if err := guardarAprobado(ctx, tk, []Repo{r}, revisados); err == nil {
		t.Fatal("con HEAD en otro commit no se aprueba")
	}
	gitCrudoT(t, r.WT, "checkout", "-q", "factory/t1")
	if err := guardarAprobado(ctx, tk, []Repo{r}, revisados); err != nil {
		t.Fatalf("de vuelta en lo fijado, se aprueba: %v", err)
	}
	if got := describeReposRevision(fijos); !strings.Contains(got, revisados["web"]) {
		t.Fatalf("el revisor recibe el commit fijado, no HEAD: %s", got)
	}
}

// 2, con srt real: el sandbox de los lectores, traducido a un perfil de srt con la escritura por defecto
// de Claude Code en sus --add-dir, deja leer el diff y no escribe en el worktree, el clon ni lo que lee,
// ni sale a la red.
func TestSandboxLectorConSrt(t *testing.T) {
	srtReal(t)
	home, r := worktreeDeTarea(t)
	base := gitCrudoT(t, r.WT, "rev-parse", "HEAD")
	sha := commitConArchivo(t, r.WT, "f.txt", "cambio\n")
	c := Config{Home: home, Factory: filepath.Join(home, "fab"), Runs: filepath.Join(home, "runs")}
	tarea := dirTareaDe(r.WT)
	origen := filepath.Join(home, "repos", "web")
	escribir(t, origen, "x.txt", "x\n")
	if err := crearDirsDeTarea(tarea); err != nil {
		t.Fatal(err)
	}
	sb := c.sandboxDeTarea(tarea, []Repo{r}, "lector", []string{r.WT, origen})
	fs := sb["filesystem"].(map[string]any)
	// Lo que Claude Code deja escribir por defecto (cwd y --add-dir); el denyWrite tiene que ganarle.
	escribe := append(fs["allowWrite"].([]string), r.WT, r.Src, origen)
	perfil := map[string]any{
		"network":    map[string]any{"allowedDomains": sb["network"].(map[string]any)["allowedDomains"], "deniedDomains": []string{}, "allowLocalBinding": false},
		"filesystem": map[string]any{"allowWrite": escribe, "denyWrite": fs["denyWrite"], "denyRead": fs["denyRead"], "allowRead": fs["allowRead"]},
	}
	b, _ := json.Marshal(perfil)
	p := filepath.Join(t.TempDir(), "lector.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	linea := `cd ` + shQuote(r.WT) + ` && git diff --text ` + base + `...` + sha + ` | grep -q '+cambio' && echo DIFF_OK
git status --porcelain >/dev/null && git log --oneline -1 >/dev/null && git blame f.txt >/dev/null && echo LEE_OK
touch ` + shQuote(filepath.Join(r.WT, "x")) + ` 2>/dev/null && echo ESCRIBE_WT
touch ` + shQuote(filepath.Join(r.Src, "x")) + ` 2>/dev/null && echo ESCRIBE_CLON
touch ` + shQuote(filepath.Join(origen, "y")) + ` 2>/dev/null && echo ESCRIBE_ORIGEN
curl -sf --max-time 10 -o /dev/null https://registry.npmjs.org && echo RED
true`
	cmd, err := comandoEncerrado(context.Background(), p, linea)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(append(cleanEnv(), entornoDeTarea(tarea)...), entornoGitLector()...) // TMPDIR de la tarea, adentro
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	s := string(out)
	for _, quiero := range []string{"DIFF_OK", "LEE_OK"} {
		if !strings.Contains(s, quiero) {
			t.Errorf("el lector no puede leer: falta %s: %s", quiero, s)
		}
	}
	for _, no := range []string{"ESCRIBE_WT", "ESCRIBE_CLON", "ESCRIBE_ORIGEN", "RED"} {
		if strings.Contains(s, no) {
			t.Errorf("el sandbox del lector dejó pasar %s", no)
		}
	}
}
