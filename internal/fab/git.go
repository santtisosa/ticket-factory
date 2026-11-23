package fab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const checkTimeout = 20 * time.Minute

// Repo es un repo destino de una tarea.
type Repo struct {
	Name  string   // el nombre del repo en fabrica.yaml
	Src   string   // el clon privado de la fábrica, en <espacio>/clones/
	WT    string   // el worktree de la tarea
	Check string   // la verificación de fabrica.yaml; vacío = se infiere
	Env   []string // las variables de los servicios de prueba del proyecto (servicios.exporta)
	// Encierro es el perfil de srt con el que corre el código de la rama (encierro.go). Vacío: no se corre.
	Encierro string
	// BaseSHA es el sha de origin/main fijado para la revisión (base.go). Vacío: el ref Base.
	BaseSHA string
	// HeadSHA es el commit de la rama fijado antes del escaneo y las revisiones (fijarRevisados): lo que
	// se revisa y lo único que se puede aprobar. Vacío: HEAD.
	HeadSHA string
}

// headDe: el commit que se revisa de r (el fijado, o HEAD antes de fijarlo).
func headDe(r Repo) string {
	if r.HeadSHA != "" {
		return r.HeadSHA
	}
	return "HEAD"
}

// Base es la rama contra la que se trabaja y se compara: el main de tu repo, leído desde el clon
// privado. La fábrica nunca crea ramas ni worktrees en los repos de erp/ (ahí trabajan otros).
const Base = "origin/main"

// gitBlindaje: lo que la fábrica fija en cada git que corre afuera del encierro, sobre worktrees y
// clones que el código de la rama puede escribir: nada de la config ejecuta un comando (fsmonitor,
// hooks, pager, ssh) y los atributos globales no cuentan. Va por GIT_CONFIG_COUNT (prioridad de línea
// de comandos), así también lo hereda el git que lanza otra herramienta (gitleaks). diff.external no se
// anula con un valor vacío (git intenta ejecutar "" y el diff falla): por eso git corre sin la config
// global ni la del sistema (entornoBlindaje), y la común del clon no se puede escribir desde el
// encierro. La identidad del rebase de aldia.go se fija aparte (identidadGit).
var gitBlindaje = [][2]string{{"core.fsmonitor", "false"}, {"core.hooksPath", "/dev/null"}, {"core.pager", "cat"},
	{"core.sshCommand", "ssh"}, {"core.attributesFile", "/dev/null"},
	// Con GIT_GRAFT_FILE fijado, git avisa en cada comando que los grafts están en desuso y el aviso
	// ensucia la salida que se lee (status --porcelain).
	{"advice.graftFileDeprecated", "false"}}

// entornoSinReemplazos: git ve los objetos tal cual están (revisión del 23/11). Con `git replace X Y`
// todo diff y escaneo mostraba Y y el push mandaba X; GIT_NO_REPLACE_OBJECTS no cubre info/grafts
// (probado con git 2.54): GIT_GRAFT_FILE va a un archivo vacío.
var entornoSinReemplazos = []string{"GIT_NO_REPLACE_OBJECTS=1", "GIT_GRAFT_FILE=/dev/null"}

// arbolVacio: el árbol vacío de SHA-1, que git conoce sin tenerlo guardado. Como GIT_ATTR_SOURCE, los
// .gitattributes de la rama no cuentan (un `* -diff` le muestra "Binary files differ" al revisor; probado).
const arbolVacio = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// identidadGit: el nombre y el correo con los que la fábrica rebasa (los del usuario, leídos una vez de
// su config global; si no hay, los de la fábrica). La config global no se usa para nada más.
var identidadGit = sync.OnceValue(func() [2]string {
	leer := func(k string) string {
		cmd := exec.Command("git", "config", "--global", "--get", k)
		cmd.Env = conEntorno(os.Environ(), []string{"GIT_CONFIG_NOSYSTEM=1"})
		out, _ := cmd.Output()
		return strings.TrimSpace(string(out))
	}
	n, e := leer("user.name"), leer("user.email")
	if n == "" || e == "" {
		return [2]string{"ticket-factory", "ticket-factory@localhost"}
	}
	return [2]string{n, e}
})

// Las raíces de los clones y de las tareas (las fija NewConfig). Un directorio bajo raizRuns es un
// worktree de tarea: git solo corre sobre él con su gitdir verificado.
var raizClones, raizRuns string

// errWorktreeAjeno: el archivo .git de un worktree no apunta a su gitdir en el clon de la fábrica.
var errWorktreeAjeno = errors.New("seguridad: el worktree no apunta a su clon")

// entornoGitSeguro: las variables para un git (o una herramienta que corre git) sobre dir: el
// blindaje y, si dir es un worktree, su gitdir verificado. Error: no se corre git ahí.
func entornoGitSeguro(dir string) ([]string, error) {
	env, err := entornoGit(dir)
	if err != nil {
		return nil, err
	}
	return append(env, entornoBlindaje()...), nil
}

// entornoBlindaje: las variables de gitBlindaje y la identidad, sin la config global ni la del sistema,
// sin reemplazos ni grafts y sin los atributos del sistema.
func entornoBlindaje() []string {
	id := identidadGit()
	kvs := append(slices.Clone(gitBlindaje), [2]string{"user.name", id[0]}, [2]string{"user.email", id[1]})
	env := append([]string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=" + strconv.Itoa(len(kvs))}, entornoSinReemplazos...)
	for i, kv := range kvs {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
	}
	return env
}

// entornoGitBuilder: el git del Builder sin reemplazos ni grafts (los suyos los ve igual que la fábrica);
// su config global queda, por la identidad de sus commits.
func entornoGitBuilder() []string {
	return append(slices.Clone(entornoSinReemplazos), "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=advice.graftFileDeprecated",
		"GIT_CONFIG_VALUE_0=false")
}

// entornoGitLector: el git de los agentes que leen (Spec writer, revisores, UX, resultado): el blindaje
// y, además, sin los .gitattributes de la rama. Lo que muestra un diff es lo que se sube.
func entornoGitLector() []string { return append(entornoBlindaje(), "GIT_ATTR_SOURCE="+arbolVacio) }

// conEntorno: base sin las variables de git que se van a fijar, más extra.
func conEntorno(base, extra []string) []string {
	var out []string
	for _, kv := range base {
		if !strings.HasPrefix(kv, "GIT_") {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	env, err := entornoGitSeguro(dir)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = conEntorno(os.Environ(), env)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, tail(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// entornoGit: para un worktree (su .git es un archivo, o el directorio está bajo raizRuns), GIT_DIR,
// GIT_COMMON_DIR y GIT_WORK_TREE explícitos y verificados, así git no sigue un .git ni un commondir
// que haya reescrito la rama. Para un clon de la fábrica (bajo raizClones), lo mismo con su propio .git,
// y si alguien le plantó un .git/commondir (git lo trataría como worktree de otro repo), no se corre.
// Para un repo común, nada.
func entornoGit(dir string) ([]string, error) {
	fi, err := os.Lstat(filepath.Join(dir, ".git"))
	enRuns := raizRuns != "" && dentroDe(dir, raizRuns)
	if err != nil {
		if enRuns {
			return nil, fmt.Errorf("%w: %s no tiene .git", errWorktreeAjeno, dir)
		}
		return nil, nil // un clon nuevo, un repo de origen: git decide
	}
	if fi.IsDir() && !enRuns {
		if raizClones == "" || !dentroDe(dir, raizClones) || mismaRuta(dir, raizClones) {
			return nil, nil
		}
		gd := filepath.Join(dir, ".git")
		if _, err := os.Lstat(filepath.Join(gd, "commondir")); err == nil {
			return nil, fmt.Errorf("%w: %s tiene .git/commondir", errWorktreeAjeno, dir)
		}
		return []string{"GIT_DIR=" + gd, "GIT_COMMON_DIR=" + gd, "GIT_WORK_TREE=" + dir}, nil
	}
	gd, comun, err := gitdirDeWorktree(dir, fi)
	if err != nil {
		return nil, err
	}
	return []string{"GIT_DIR=" + gd, "GIT_COMMON_DIR=" + comun, "GIT_WORK_TREE=" + dir}, nil
}

// gitdirDeWorktree: el .git de dir es un archivo regular "gitdir: <clon>/.git/worktrees/<n>", con el
// clon bajo raizClones (si está fijada), y ese gitdir apunta de vuelta a dir/.git. Devuelve el gitdir
// y el directorio común (<clon>/.git).
func gitdirDeWorktree(dir string, fi os.FileInfo) (string, string, error) {
	mal := func(motivo string) (string, string, error) {
		return "", "", fmt.Errorf("%w: %s: %s", errWorktreeAjeno, dir, motivo)
	}
	if !fi.Mode().IsRegular() {
		return mal(".git no es un archivo regular")
	}
	b, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return mal(err.Error())
	}
	gd, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
	if !ok || strings.Contains(gd, "\n") {
		return mal(".git sin una línea gitdir")
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(dir, gd)
	}
	gd = filepath.Clean(gd)
	comun := filepath.Dir(filepath.Dir(gd))
	clon := filepath.Dir(comun)
	if filepath.Base(filepath.Dir(gd)) != "worktrees" || filepath.Base(comun) != ".git" {
		return mal("gitdir fuera de <clon>/.git/worktrees: " + gd)
	}
	if raizClones != "" && !mismaRuta(filepath.Dir(clon), raizClones) {
		return mal("el clon " + clon + " no es de la fábrica")
	}
	for _, d := range []string{comun, gd} {
		if st, err := os.Lstat(d); err != nil || !st.IsDir() {
			return mal(d + " no es un directorio")
		}
	}
	// Con extensions.worktreeConfig, git leería gd/config.worktree, que la rama puede escribir. La
	// fábrica nunca lo activa: si aparece, no se sigue.
	if cfg, err := os.ReadFile(filepath.Join(comun, "config")); err != nil || strings.Contains(strings.ToLower(string(cfg)), "worktreeconfig") {
		return mal("la config del clon activa config.worktree o no se puede leer")
	}
	// El commondir tiene que ser el de git worktree add: el ref store de git lo sigue aunque se fije
	// GIT_COMMON_DIR (probado con git 2.54), así que se verifica, y el encierro no lo puede escribir.
	if cd, err := os.ReadFile(filepath.Join(gd, "commondir")); err != nil || !mismaRuta(rutaDesde(gd, strings.TrimSpace(string(cd))), comun) {
		return mal("el commondir de " + gd + " no apunta a " + comun)
	}
	atras, err := os.ReadFile(filepath.Join(gd, "gitdir"))
	if err != nil || !mismaRuta(strings.TrimSpace(string(atras)), filepath.Join(dir, ".git")) {
		return mal("el gitdir " + gd + " es de otro worktree")
	}
	return gd, comun, nil
}

// VerificarWorktrees: los worktrees de la tarea siguen apuntando a sus clones. Lo corre la línea al
// terminar cada vuelta; un error es de seguridad y traba la tarea.
func VerificarWorktrees(repos []Repo) error {
	for _, r := range repos {
		fi, err := os.Lstat(filepath.Join(r.WT, ".git"))
		if err != nil {
			return fmt.Errorf("%w: %s: %v", errWorktreeAjeno, r.WT, err)
		}
		gd, _, err := gitdirDeWorktree(r.WT, fi)
		if err != nil {
			return err
		}
		if !mismaRuta(filepath.Dir(filepath.Dir(filepath.Dir(gd))), r.Src) {
			return fmt.Errorf("%w: %s apunta a %s, no a %s", errWorktreeAjeno, r.WT, gd, r.Src)
		}
	}
	return nil
}

// errGitAlterado: el clon tiene algo que cambia lo que git muestra respecto de lo que se sube.
var errGitAlterado = errors.New("seguridad: el clon tiene reemplazos, grafts, alternates o atributos plantados")

// archivosQueEnganan: los archivos del .git de un clon que hacen que un diff o un escaneo vean otra cosa
// que la que sube el push (revisión del 23/11): alternates (objetos de otro lado), grafts y shallow
// (otra historia) e info/attributes (atributos por encima de los del repo). La fábrica nunca los crea.
var archivosQueEnganan = []string{"objects/info/alternates", "objects/info/http-alternates", "info/grafts", "info/attributes", "shallow"}

// rutasGitProtegidas: lo que nadie fuera de la fábrica escribe en el .git de un clon: hooks, config y
// commondir (git los usa afuera del encierro), packed-refs (ahí también viven las refs de otras tareas),
// info y objects/info (grafts, atributos, alternates), refs/replace y shallow. Las usan el perfil de srt
// y el sandbox del Builder. Adentro, commit, rebase y amend andan, y un commit con gc.auto=1 termina en 0;
// un `git gc` o `git repack` explícito falla (probado con srt 0.0.83 y git 2.54).
func rutasGitProtegidas(clon string) []string {
	gd := filepath.Join(clon, ".git")
	var ps []string
	for _, p := range []string{"hooks", "config", "commondir", "packed-refs", "info", "objects/info", "refs/replace", "shallow"} {
		ps = append(ps, filepath.Join(gd, p))
	}
	return ps
}

// integridadGit: el clon no tiene refs de reemplazo ni ninguno de archivosQueEnganan con contenido. Lo
// exigen guardar lo aprobado y la aprobación: con cualquiera de ellos, lo revisado no es lo que se sube.
func integridadGit(ctx context.Context, clon string) error {
	gd := filepath.Join(clon, ".git")
	for _, p := range archivosQueEnganan {
		b, err := os.ReadFile(filepath.Join(gd, p))
		switch {
		case err == nil && strings.TrimSpace(string(b)) != "":
			return fmt.Errorf("%w: .git/%s", errGitAlterado, p)
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("%w: no se pudo leer .git/%s: %v", errGitAlterado, p, err)
		}
	}
	out, err := git(ctx, clon, "for-each-ref", "--format=%(refname)", "refs/replace/")
	if err != nil {
		return err
	}
	if out != "" {
		return fmt.Errorf("%w: %s", errGitAlterado, strings.ReplaceAll(out, "\n", ", "))
	}
	return nil
}

// rutaDesde: p absoluta, o relativa a dir.
func rutaDesde(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

// mismaRuta compara rutas resolviendo enlaces (git escribe rutas reales: /private/var en macOS).
func mismaRuta(a, b string) bool {
	real := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return real(a) == real(b)
}

// dentroDe: p está dentro de raiz (o es raiz).
func dentroDe(p, raiz string) bool {
	rel, err := filepath.Rel(raiz, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// gitCrudo: git con el blindaje y la salida sin recortar (stdout).
func gitCrudo(ctx context.Context, dir string, args ...string) ([]byte, error) {
	env, err := entornoGitSeguro(dir)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = conEntorno(os.Environ(), env)
	return cmd.Output()
}

// Branch es la rama de la tarea; vive solo en el clon privado.
func Branch(id string) string { return "factory/" + id }

// NoPush es la URL de push del origin de cada clon: no existe, así que ningún `git push` que
// salga del clon llega a ningún lado (ni al repo de erp/ ni a GitHub). La aprobación empuja a una URL
// explícita y no pasa por acá.
const NoPush = "no-push://fabrica-no-pushea"

// ensureClone crea (una vez) el clon privado desde el repo de erp/ y trae su main. Solo lee el
// repo de origen.
func ensureClone(ctx context.Context, origin, clone string) error {
	if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
		var cmd *exec.Cmd
		if esRemotoGitHub(origin) {
			url, err := urlGitHub(origin)
			if err != nil {
				return err
			}
			if cmd, err = comandoGitGitHub(ctx, "", "clone", "--quiet", url, clone); err != nil {
				return fmt.Errorf("clonar %s: %w", origin, err)
			}
		} else {
			// El repo de origen es local: git con el blindaje, sin seguir su config.
			cmd = exec.CommandContext(ctx, "git", "clone", "--quiet", origin, clone)
			cmd.Env = conEntorno(os.Environ(), entornoBlindaje())
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("clonar %s: %w: %s", origin, err, tail(string(out)))
		}
	}
	// En cada pasada, por si el clon es de antes de esta regla o alguien la tocó.
	if _, err := git(ctx, clone, "config", "remote.origin.pushurl", NoPush); err != nil {
		return fmt.Errorf("bloquear el push del clon: %w", err)
	}
	// La base es el main de GitHub, no el checkout local (puede estar viejo o en otra rama). Sin red,
	// el main del repo local, para no frenar.
	const ref = "+main:refs/remotes/origin/main"
	gh := origin
	if !esRemotoGitHub(gh) {
		gh, _ = git(ctx, origin, "remote", "get-url", "origin")
	}
	if esRemotoGitHub(gh) {
		url, err := urlGitHub(gh)
		if err != nil {
			return err
		}
		// Con el token de la fábrica y sin TTY: ni las credenciales del usuario ni un prompt.
		fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		defer cancel()
		cmd, err := comandoGitGitHub(fctx, clone, "fetch", "--quiet", url, ref)
		if err != nil {
			return fmt.Errorf("traer main de GitHub: %w", err) // sin token no cae al checkout local en silencio
		}
		enGrupo(cmd)
		if cmd.Run() == nil {
			return nil
		}
		if esRemotoGitHub(origin) {
			return fmt.Errorf("no se pudo traer main de %s", url)
		}
	}
	_, err := git(ctx, clone, "fetch", "--quiet", "origin", ref)
	return err
}

// AddWorktree crea el worktree de la tarea en el clon privado, desde origin/main. Lo crea la
// fábrica, no `isolation: worktree`, que sale de la rama por defecto y no se sabe si aplica con --agent.
func AddWorktree(ctx context.Context, r *Repo, origin, taskDir, id string) error {
	if err := ensureClone(ctx, origin, r.Src); err != nil {
		return err
	}
	if _, err := git(ctx, r.Src, "rev-parse", "--verify", "--quiet", "refs/heads/"+Branch(id)); err == nil {
		// Reanudación: la rama ya existe con lo que hizo el Builder; se monta, no se pisa.
		r.WT = filepath.Join(taskDir, "wt", r.Name)
		if _, err := os.Stat(r.WT); err == nil {
			return nil
		}
		_, err := git(ctx, r.Src, "worktree", "add", "--quiet", r.WT, Branch(id))
		return err
	}
	return addWorktree(ctx, r, origin, taskDir, "-b", Branch(id))
}

// AddBaseWorktree: un repo que la tarea no toca pero la app necesita (ej. el backend para UX),
// en origin/main y sin rama.
func AddBaseWorktree(ctx context.Context, r *Repo, origin, taskDir string) error {
	return addWorktree(ctx, r, origin, taskDir, "--detach")
}

func addWorktree(ctx context.Context, r *Repo, origin, taskDir string, mode ...string) error {
	r.WT = filepath.Join(taskDir, "wt", r.Name)
	if _, err := os.Stat(r.WT); err == nil {
		return nil // reanudación: ya existe
	}
	if err := ensureClone(ctx, origin, r.Src); err != nil {
		return err
	}
	args := append(append([]string{"worktree", "add", "--quiet"}, mode...), r.WT, Base)
	_, err := git(ctx, r.Src, args...)
	return err
}

// Sucios devuelve lo que quedó sin commitear en el worktree. Lo que no está commiteado no lo ven
// los revisores ni lo sube la aprobación: si queda algo, la vuelta no pasa.
func Sucios(ctx context.Context, r Repo) ([]string, error) {
	out, err := git(ctx, r.WT, "status", "--porcelain")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil, err
	}
	return strings.Split(strings.TrimSpace(out), "\n"), nil
}

// Head devuelve el commit actual del worktree.
func Head(ctx context.Context, r Repo) (string, error) { return git(ctx, r.WT, "rev-parse", "HEAD") }

// ChangedSince lista los archivos que cambiaron desde un commit (para congelar tests).
func ChangedSince(ctx context.Context, r Repo, sha string) ([]string, error) {
	out, err := git(ctx, r.WT, "diff", "--name-only", sha, headDe(r))
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// DiffStat resume lo que cambió en la rama contra main.
func DiffStat(ctx context.Context, r Repo) string {
	out, err := git(ctx, r.WT, "diff", "--stat", baseDe(r)+"...HEAD")
	if err != nil {
		return err.Error()
	}
	return out
}

// IsAcceptanceTest: los tests de aceptación quedan congelados después de la primera vuelta.
func IsAcceptanceTest(path string) bool {
	base := filepath.Base(path)
	// _aceptacion_test.go también: el Builder lo escribe en castellano y rechazarlo costó una vuelta
	// entera con el código bien (intern 49a1, 04/11).
	return strings.HasSuffix(base, "_acceptance_test.go") || strings.HasSuffix(base, "_aceptacion_test.go") ||
		strings.Contains(base, ".aceptacion.test.")
}

// CheckCmd es el chequeo local de cada repo: lint + unitarios, como `make check`.
// ponytail: npm ci por worktree es lento; compartir node_modules si molesta.
// Sale del repo: `make check` si el Makefile lo tiene; si no, lo que cada stack corre siempre.
func CheckCmd(r Repo) string {
	// --ignore-scripts: el postinstall de una dependencia nueva no corre en la máquina.
	pre := ""
	if _, err := os.Stat(filepath.Join(r.WT, "package.json")); err == nil {
		pre = "[ -d node_modules ] || npm ci --ignore-scripts --silent; "
	}
	if strings.TrimSpace(r.Check) != "" {
		return r.Check
	}
	switch {
	case tieneTarget(r.WT, "check"):
		return pre + "make check"
	case tieneTarget(r.WT, "test-unit"):
		return pre + "make lint test-unit"
	case esGo(r.WT):
		return "go vet ./... && go test -short ./..."
	case tieneScriptNpm(r.WT, "check"):
		// El `check` del repo es lo mismo que corren los jobs rápidos de su CI: con solo `npm test`,
		// el CI puede caer en un lint que la fábrica no corrió.
		return pre + "npm run check"
	}
	// Repos sin `check`: no todos tienen lint o test.
	return pre + "npm run --if-present lint && npm run --if-present test -- --silent"
}

// tieneScriptNpm: el package.json del repo define ese script.
func tieneScriptNpm(dir, script string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	return json.Unmarshal(b, &pkg) == nil && pkg.Scripts[script] != ""
}

// tieneTarget: el Makefile del repo define ese target.
func tieneTarget(dir, target string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "Makefile"))
	if err != nil {
		return false
	}
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:`).Match(b)
}

// motivoCortado: un chequeo que llegó al tope no es un test rojo. El texto lo reconoce infraRe, así
// no le gasta una vuelta al Builder.
func motivoCortado(repo string) string {
	return fmt.Sprintf("chequeo de %s: se cortó por tiempo tras %d min (tope de la fábrica, no un test rojo)",
		repo, int(checkTimeout.Minutes()))
}

// salidaChequeo: cuánto de la salida del chequeo rojo vuelve al Builder. Con 600 bytes la vuelta 2
// arreglaba a ciegas: al diff del golden de 5374 se le escaparon el dashboard y una alerta.
const salidaChequeo = 8000

// RunCheck corre el chequeo dentro de srt (r.Encierro) y devuelve si pasó y la cola de la salida. Sin
// encierro no corre nada y el motivo es de infraestructura (infraRe), no un chequeo rojo.
func RunCheck(ctx context.Context, r Repo) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var completa string // la salida entera de la última corrida, para ver qué archivos fallaron
	correr := func(linea string) (bool, string) {
		cmd, err := comandoEncerrado(ctx, r.Encierro, linea)
		if err != nil {
			completa = ""
			return false, "chequeo de " + r.Name + ": " + err.Error()
		}
		enGrupo(cmd)
		cmd.Dir = r.WT
		// El chequeo corre código del Builder: sin secretos, con el temporal y las cachés de la tarea.
		cmd.Env = append(append(cleanEnv(), r.Env...), entornoDeTarea(dirTareaDe(r.WT))...)
		out, err := cmd.CombinedOutput()
		matarGrupo(cmd) // lo que quedó de fondo no sobrevive al chequeo
		completa = string(out)
		return err == nil, tailN(completa, salidaChequeo)
	}
	if r.Encierro != "" {
		if err := crearDirsDeTarea(dirTareaDe(r.WT)); err != nil {
			return false, "chequeo de " + r.Name + ": " + err.Error()
		}
		bajarModulos(ctx, r.Encierro, r.WT)
	}
	ok, out := correr(CheckCmd(r))
	// Un test inestable del repo no le cuesta una vuelta al
	// Builder: el chequeo corre otra vez y solo cuenta si falla las dos.
	if !ok && ctx.Err() == nil {
		ok, out = correr(CheckCmd(r))
	}
	// Bajo la carga de la suite entera un test ajeno de jsdom se queda sin tiempo las dos veces y
	// solo pasa. Se corren solos los archivos de jest que fallaron
	// y que la tarea no tocó: si pasan, no cuentan.
	if !ok && ctx.Err() == nil {
		if ajenos := jestFallidosAjenos(ctx, r, completa); len(ajenos) > 0 {
			rojo := completa
			if solo, _ := correr("npx jest " + strings.Join(ajenos, " ")); solo && soloFallaronEsos(rojo, ajenos) {
				// Lo que el check no llegó a correr porque jest cortó (el typecheck, si el repo lo tiene).
				if resto, outResto := correr("npm run --if-present typecheck"); resto {
					ok, out = true, out+"\n(los tests rojos pasaron solos, fuera de la carga de la suite: "+strings.Join(ajenos, ", ")+")"
				} else {
					out = outResto
				}
			}
		}
	}
	if !ok {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return false, motivoCortado(r.Name)
		}
		return false, out
	}
	if s := SemgrepCmd(r); s != "" {
		return correr(s)
	}
	return true, out
}

var reJestFail = regexp.MustCompile(`(?m)^FAIL\s+(\S+\.(?:test|spec)\.[jt]sx?)\b`)

// jestFallidosAjenos: los archivos que jest marcó FAIL y que la tarea no cambió (los de la tarea
// tienen que pasar en la suite, no solos).
func jestFallidosAjenos(ctx context.Context, r Repo, salida string) []string {
	cambiados := map[string]bool{}
	if lista, err := ChangedSince(ctx, r, baseDe(r)); err == nil {
		for _, f := range lista {
			cambiados[f] = true
		}
	}
	var res []string
	for _, m := range reJestFail.FindAllStringSubmatch(salida, -1) {
		if !cambiados[m[1]] {
			res = append(res, m[1])
		}
	}
	return res
}

// soloFallaronEsos: la corrida roja no tuvo otra falla que esos archivos (ni lint, ni tipos, ni un
// test de la tarea): jest dice "Test Suites: N failed" con N igual a los archivos.
func soloFallaronEsos(salida string, archivos []string) bool {
	m := regexp.MustCompile(`Test Suites: (\d+) failed`).FindStringSubmatch(salida)
	return m != nil && m[1] == strconv.Itoa(len(archivos)) && len(reJestFail.FindAllString(salida, -1)) == len(archivos)
}

// SemgrepCmd: las reglas propias del repo (.semgrep), solo sobre lo nuevo respecto de main, como
// el job de seguridad del CI. Sin esto la PR de intern 49a1 llegó roja al CI por un UPDATE sin
// RowsAffected que la fábrica había aprobado. Vacío si el repo no tiene reglas o falta semgrep.
func SemgrepCmd(r Repo) string {
	if _, err := os.Stat(filepath.Join(r.WT, ".semgrep")); err != nil {
		return ""
	}
	bin, err := exec.LookPath("semgrep")
	if err != nil {
		return ""
	}
	return SemgrepCmdCon(r, bin)
}

// SemgrepCmdCon: la línea de semgrep con ese binario, contra la base fija de la tarea (base.go).
func SemgrepCmdCon(r Repo, bin string) string {
	// Ruta absoluta: el chequeo corre con cleanEnv y no depende de su PATH. Sin chequeo de versión:
	// ni en el sandbox del Builder ni en srt semgrep.dev está en la lista de red.
	return "SEMGREP_ENABLE_VERSION_CHECK=0 " + strconv.Quote(bin) + " scan --config .semgrep --baseline-commit " + baseDe(r) + " --error --quiet --metrics=off"
}

func esGo(wt string) bool {
	_, err := os.Stat(filepath.Join(wt, "go.mod"))
	return err == nil
}
