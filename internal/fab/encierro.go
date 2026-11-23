package fab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// El encierro del código de la rama. El chequeo (RunCheck), el hook Stop del Builder y la app de la
// vista previa y de UX/UI ejecutan código que escribió un agente, fuera del sandbox de Claude Code. En
// esta máquina git y gh guardan credenciales en el llavero: ese código podría pedirlas con `git
// credential fill` y publicar sin aprobación. Limpiar el entorno no alcanza; el control es la red.
// Por eso todo corre dentro de srt (sandbox runtime de Anthropic) con un perfil que genera la fábrica:
// red solo a los registros de dependencias y a localhost (nunca github.com), escritura solo en los
// worktrees, los clones de la tarea y el temporal y las cachés de la tarea, y sin leer credenciales, el
// llavero ni el resto de ~/Documents. Sin srt no se corre nada.
// Docker no está adentro (el socket queda bloqueado): los servicios los levanta la fábrica desde
// fabrica.yaml (servicios.go).

// instalarSrt: cómo se resuelve la falta de srt. Lo reconoce infraRe.
const instalarSrt = "sin encierro (srt): instale el sandbox runtime con `npm install -g @anthropic-ai/sandbox-runtime` y verifique que `srt --version` responda"

// esperaSondaSrt: cuánto puede tardar srt en arrancar para la sonda.
const esperaSondaSrt = time.Minute

// srtRuta encuentra el binario de srt (variable para los tests).
var srtRuta = func() (string, error) { return exec.LookPath("srt") }

var errSinEncierro = errors.New(instalarSrt)

// dominiosDependencias: la red que tiene el código de la rama y el Builder. Sin github.com.
func dominiosDependencias() []string {
	return []string{"registry.npmjs.org", "proxy.golang.org", "sum.golang.org"}
}

// dominiosHerramientas: la red de las herramientas de la etapa determinista (escaneo.go) y de la
// descarga de módulos de Go: las dependencias más la base de vulnerabilidades de Go, el registro de
// reglas de semgrep y storage.googleapis.com, adonde proxy.golang.org redirige los zips grandes con
// una URL firmada (sin él, modernc.org/sqlite no baja; probado). Ninguna de esas herramientas ejecuta
// código de la rama: el Builder y el chequeo no tienen ese dominio.
func dominiosHerramientas() []string {
	return append(dominiosDependencias(), "vuln.go.dev", "semgrep.dev", "storage.googleapis.com")
}

// dirTareaDe: la carpeta de la tarea (runs/<id>, o la de probar-app) de un worktree <tarea>/wt/<repo>.
func dirTareaDe(wt string) string { return filepath.Dir(filepath.Dir(wt)) }

// dirsDeTarea: el temporal y las cachés propios de la tarea. Las escribe el código de la rama (srt) y el
// Builder; la fábrica nunca ejecuta nada desde ahí.
func dirsDeTarea(dirTarea string) (tmp, cache string) {
	return filepath.Join(dirTarea, "tmp"), filepath.Join(dirTarea, "cache")
}

// entornoDeTarea: el temporal (FABRICA_TMPDIR, que la línea encerrada exporta como TMPDIR: srt mismo
// pone su socket en TMPDIR y con la ruta de la tarea puede pasar el largo máximo; probado) y las cachés
// de Go, npm, XDG y semgrep de la tarea, más Go sin toolchains descargadas ni otra red que el proxy. Va
// al final de cmd.Env: pisa lo que traiga envPermitido.
func entornoDeTarea(dirTarea string) []string {
	tmp, cache := dirsDeTarea(dirTarea)
	c := func(p string) string { return filepath.Join(cache, p) }
	return []string{"FABRICA_TMPDIR=" + tmp, "GOPATH=" + c("gopath"), "GOMODCACHE=" + c("gomod"), "GOCACHE=" + c("gobuild"),
		"GOTOOLCHAIN=local", "GOENV=off", "GOPROXY=https://proxy.golang.org", "GOVCS=*:off",
		// La caché de módulos de la tarea, borrable con la tarea (Go la deja de solo lectura).
		"GOFLAGS=" + strings.TrimSpace(os.Getenv("GOFLAGS")+" -modcacherw"),
		"npm_config_cache=" + c("npm"), "XDG_CACHE_HOME=" + c("xdg"),
		"SEMGREP_SETTINGS_FILE=" + c("semgrep/settings.yml"), "SEMGREP_LOG_FILE=" + c("semgrep/semgrep.log"),
		"SEMGREP_VERSION_CACHE_PATH=" + c("semgrep/version"), "SEMGREP_ENABLE_VERSION_CHECK=0"}
}

// entornoDeAgente: lo de entornoDeTarea más TMPDIR, para un agente (su sandbox deja escribir en el
// TMPDIR del proceso: así es el de la tarea, no el del usuario).
func entornoDeAgente(dirTarea string) []string {
	tmp, _ := dirsDeTarea(dirTarea)
	return append(entornoDeTarea(dirTarea), "TMPDIR="+tmp)
}

// crearDirsDeTarea crea el temporal y las cachés de la tarea.
func crearDirsDeTarea(dirTarea string) error {
	tmp, cache := dirsDeTarea(dirTarea)
	for _, d := range []string{tmp, filepath.Join(cache, "semgrep")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("carpetas de la tarea: %w", err)
		}
	}
	return nil
}

// telemetriaGo: lo único de ~/Library/Application Support que se deja leer (go la consulta en cada
// corrida y sin ella ensucia la salida con un error).
func telemetriaGo() string {
	return filepath.Join(homeUsuario(), "Library", "Application Support", "go", "telemetry")
}

// perfilEncierro es el JSON de srt para el código de esos repos: la única fuente del encierro.
// herramientas: el perfil de la etapa determinista (más red, ver dominiosHerramientas, y trustd para
// que Go verifique TLS; sin escribir en los worktrees ni en los clones). El de código escribe en la
// carpeta de los worktrees y en el clon de cada repo (el índice del worktree vive ahí), nunca en los
// hooks, la config ni el commondir de los clones, que la fábrica usa afuera.
func (c Config) perfilEncierro(dirTarea string, repos []Repo, herramientas bool) map[string]any {
	tmp, cache := dirsDeTarea(dirTarea)
	escribe := []string{tmp, cache}
	niega := []string{filepath.Join(c.Factory, "fabrica"), filepath.Join(c.Home, ArchivoEspacio),
		filepath.Join(c.Runs, "*", "*.json"), filepath.Join(c.Runs, ".cache"), c.dirAgentes(),
		// Lo que srt deja escribir sin listarlo y no hace falta.
		"/tmp/claude", "/private/tmp/claude", filepath.Join(homeUsuario(), ".npm")}
	lee := []string{dirTarea, telemetriaGo()}
	carpetas := map[string]bool{}
	for _, r := range repos {
		if r.WT != "" && !carpetas[filepath.Dir(r.WT)] {
			wts := filepath.Dir(r.WT)
			carpetas[wts] = true
			if !herramientas {
				escribe = append(escribe, wts)
			}
			// Lo que Claude Code carga de un directorio (C3): nadie lo planta en un worktree.
			niega = append(niega, filepath.Join(wts, "*", ".claude"), filepath.Join(wts, "*", "CLAUDE.local.md"))
		}
		if r.WT != "" {
			niega = append(niega, filepath.Join(r.WT, ".git"), filepath.Join(r.WT, ".claude"), filepath.Join(r.WT, "CLAUDE.local.md"))
			// Las rutas literales del gitdir real: srt niega también mover sus carpetas padre (con el
			// glob de abajo, el gitdir entero se podía renombrar; probado).
			if fi, err := os.Lstat(filepath.Join(r.WT, ".git")); err == nil {
				if gd, _, err := gitdirDeWorktree(r.WT, fi); err == nil {
					niega = append(niega, filepath.Join(gd, "commondir"), filepath.Join(gd, "gitdir"), filepath.Join(gd, "config.worktree"))
				}
			}
		}
		if r.Src != "" && !slices.Contains(lee, r.Src) {
			lee = append(lee, r.Src)
			if !herramientas {
				escribe = append(escribe, r.Src)
			}
			wts := filepath.Join(r.Src, ".git", "worktrees", "*")
			// .git/commondir de un clon: con él, git trataría el clon como worktree de otro repo (H1). Los
			// reemplazos, grafts, alternates y atributos plantados, y las refs y gitdirs de otras tareas del
			// mismo clon (revisión del 23/11).
			niega = append(niega, rutasGitProtegidas(r.Src)...)
			niega = append(niega, filepath.Join(wts, "commondir"), filepath.Join(wts, "gitdir"), filepath.Join(wts, "config.worktree"))
			niega = append(niega, rastrosDeOtrasTareas(r.Src, dirTarea)...)
		}
	}
	// Los clones de los otros repos del cliente: los worktrees hermanos (en main) apuntan ahí.
	lee = append(lee, c.clonesDelCliente(repos)...)
	if herramientas {
		lee = append(lee, c.dirHerramientas()) // la config de gitleaks de la fábrica
	}
	dominios := dominiosDependencias()
	if herramientas {
		dominios = dominiosHerramientas()
	}
	p := map[string]any{
		"network": map[string]any{
			"allowedDomains": dominios, "deniedDomains": []string{},
			"allowLocalBinding": !herramientas, // los servicios de prueba y la app, en 127.0.0.1
		},
		"filesystem": map[string]any{"allowWrite": escribe, "denyWrite": conRutaReal(niega),
			"denyRead": append(c.credenciales(), lecturaNegada()...), "allowRead": lee},
		// Explícito aunque sea el valor por defecto: sin Apple Events ni Launch Services (open, osascript).
		"allowAppleEvents": false,
	}
	if herramientas {
		// Go en macOS verifica TLS con trustd (sin esto: x509 OSStatus -26276; probado). Solo en este
		// perfil, que no ejecuta código de la rama.
		p["enableWeakerNetworkIsolation"] = true
	}
	return p
}

// rastrosDeOtrasTareas: en un clon que comparten varias tareas del mismo repo, el gitdir del worktree y
// la ref de la rama de cada tarea que no es la de dirTarea. Se calcula cada vez que se escribe un perfil
// o un settings (cada vuelta y cada vista previa): lo que nazca después queda escribible hasta la
// siguiente. Las refs empaquetadas no hace falta listarlas: packed-refs entero está protegido.
func rastrosDeOtrasTareas(clon, dirTarea string) []string {
	gd := filepath.Join(clon, ".git")
	var ps []string
	if es, err := os.ReadDir(filepath.Join(gd, "worktrees")); err == nil {
		for _, e := range es {
			d := filepath.Join(gd, "worktrees", e.Name())
			b, err := os.ReadFile(filepath.Join(d, "gitdir"))
			if err != nil || !dentroDe(realOIgual(strings.TrimSpace(string(b))), realOIgual(dirTarea)) {
				ps = append(ps, d)
			}
		}
	}
	propia := Branch(filepath.Base(dirTarea))
	for _, rama := range ramasDeTareas(gd) {
		if rama != propia {
			ps = append(ps, filepath.Join(gd, "refs", "heads", rama))
		}
	}
	return ps
}

// ramasDeTareas: las ramas factory/<id> del clon, sueltas o empaquetadas.
func ramasDeTareas(gd string) []string {
	var ramas []string
	sumar := func(r string) {
		if strings.HasPrefix(r, Branch("")) && !slices.Contains(ramas, r) {
			ramas = append(ramas, r)
		}
	}
	if es, err := os.ReadDir(filepath.Join(gd, "refs", "heads", Branch(""))); err == nil {
		for _, e := range es {
			sumar(Branch(e.Name()))
		}
	}
	if b, err := os.ReadFile(filepath.Join(gd, "packed-refs")); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if _, ref, ok := strings.Cut(l, " refs/heads/"); ok {
				sumar(ref)
			}
		}
	}
	return ramas
}

// realOIgual: la ruta resuelta si existe (git escribe /private/var), o la misma.
func realOIgual(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// conRutaReal: cada ruta tal cual y, si su carpeta es un enlace, también resuelta (EvalSymlinks). srt
// resuelve las rutas que existen, pero no la de un archivo que todavía no está (un .git/commondir por
// plantar): bajo /var (que en macOS es /private/var) el deny no alcanzaba (probado). Las rutas con
// comodines quedan como están.
func conRutaReal(ps []string) []string {
	var out []string
	sumar := func(p string) {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	for _, p := range ps {
		sumar(p)
		if strings.ContainsAny(p, "*?[") {
			continue
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			sumar(r)
		} else if d, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
			sumar(filepath.Join(d, filepath.Base(p)))
		}
	}
	return out
}

// clonesDelCliente: los clones de todos los repos de los clientes de esos repos.
func (c Config) clonesDelCliente(repos []Repo) []string {
	cs := Clientes(c.Home)
	var out []string
	for _, r := range repos {
		_, slug, ok := buscarRepo(cs, r.Name)
		if !ok {
			continue
		}
		cl, _ := buscarCliente(cs, slug)
		for _, rc := range cl.Repos {
			if p := filepath.Join(c.Home, "clones", rc.Nombre); !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// perfilHerramientasDe: el perfil de herramientas que acompaña a un perfil de código.
func perfilHerramientasDe(perfil string) string {
	if perfil == "" {
		return ""
	}
	return strings.TrimSuffix(perfil, ".json") + "-herramientas.json"
}

// sondaNegativa: dentro del encierro, GitHub no responde y el home del usuario no se escribe. Si algo
// de eso pasa, el perfil no aísla y no se corre nada.
const sondaNegativa = `command -v curl >/dev/null || echo FABRICA_SIN_CURL
if curl -sf --max-time 10 -o /dev/null https://github.com; then echo FABRICA_RED_GITHUB; fi
s="$HOME/.fabrica-sonda-$$"
if (: > "$s") 2>/dev/null; then rm -f "$s"; echo FABRICA_ESCRIBE_HOME; fi
true`

// sonda es la línea de sondear (variable para los tests: el srt falso de TestMain no aísla).
var sonda = sondaNegativa

// prepararEncierro escribe los perfiles de srt de esos repos (el del código y el de herramientas) en
// runs/.cache/srt-<nombre>*.json, crea el temporal y las cachés de la tarea y comprueba que srt aísle
// con cada perfil: que arranque y que la sonda negativa falle. Devuelve la ruta del perfil de código.
// Un error es de infraestructura: sin encierro, el código de la rama no se ejecuta.
func (c Config) prepararEncierro(ctx context.Context, nombre, dirTarea string, repos []Repo) (string, error) {
	if err := crearDirsDeTarea(dirTarea); err != nil {
		return "", err
	}
	perfil := filepath.Join(c.Runs, ".cache", "srt-"+nombre+".json")
	for ruta, herr := range map[string]bool{perfil: false, perfilHerramientasDe(perfil): true} {
		b, err := json.MarshalIndent(c.perfilEncierro(dirTarea, repos, herr), "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(ruta, b, 0o644); err != nil {
			return "", fmt.Errorf("perfil de srt: %w", err)
		}
		if err := sondear(ctx, ruta); err != nil {
			return "", err
		}
	}
	return perfil, nil
}

// sondear: srt arranca con el perfil y la sonda negativa no encuentra salida.
func sondear(ctx context.Context, perfil string) error {
	sctx, cancel := context.WithTimeout(ctx, esperaSondaSrt)
	defer cancel()
	cmd, err := comandoEncerrado(sctx, perfil, sonda)
	if err != nil {
		return err
	}
	enGrupo(cmd)
	cmd.Env = cleanEnv()
	out, err := cmd.CombinedOutput()
	matarGrupo(cmd)
	if err != nil {
		return fmt.Errorf("%s; srt no pudo aislar: %v: %s", instalarSrt, err, tail(string(out)))
	}
	for _, marca := range []string{"FABRICA_SIN_CURL", "FABRICA_RED_GITHUB", "FABRICA_ESCRIBE_HOME"} {
		if strings.Contains(string(out), marca) {
			return fmt.Errorf("%s; el perfil %s no aísla (%s): no se ejecuta nada", instalarSrt, filepath.Base(perfil), marca)
		}
	}
	return nil
}

// comandoEncerrado arma `srt --settings <perfil> -c "bash -c '<linea>'"`. Sin srt o sin perfil no
// hay comando: nunca se cae a ejecutar sin encierro.
func comandoEncerrado(ctx context.Context, perfil, linea string) (*exec.Cmd, error) {
	srt, args, err := argvEncerrado(perfil, linea)
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, srt, args...), nil
}

func argvEncerrado(perfil, linea string) (string, []string, error) {
	if perfil == "" {
		return "", nil, errSinEncierro
	}
	srt, err := srtRuta()
	if err != nil {
		return "", nil, errSinEncierro
	}
	return srt, []string{"--settings", perfil, "-c", "bash -c " + shQuote(conTmpDeTarea+linea)}, nil
}

// conTmpDeTarea: adentro del encierro, TMPDIR es el temporal de la tarea (H3).
const conTmpDeTarea = "if [ -n \"${FABRICA_TMPDIR:-}\" ]; then export TMPDIR=\"$FABRICA_TMPDIR\"; fi\n"

// lineaEncerrada es lo mismo que comandoEncerrado como una línea de shell, para el hook Stop
// (FABRICA_CHECK_ENCERRADO).
func lineaEncerrada(perfil, linea string) (string, error) {
	srt, args, err := argvEncerrado(perfil, linea)
	if err != nil {
		return "", err
	}
	// srt mismo usa el temporal de afuera (el del Builder es el de la tarea y su ruta puede ser larga).
	partes := []string{"TMPDIR=" + shQuote(filepath.Clean(os.TempDir())), shQuote(srt)}
	for _, a := range args {
		partes = append(partes, shQuote(a))
	}
	return strings.Join(partes, " "), nil
}

// shQuote: s entre comillas simples para sh y bash.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// dirTmpFabrica: el temporal de la fábrica (runs/.cache/tmp), que ni srt ni los agentes escriben. Los
// archivos y carpetas temporales de la fábrica van ahí con os.MkdirTemp/os.CreateTemp.
func dirTmpFabrica() string {
	if raizRuns == "" {
		return os.TempDir()
	}
	d := filepath.Join(raizRuns, ".cache", "tmp")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return os.TempDir()
	}
	return d
}

// bajarModulos descarga los módulos de Go de esos worktrees a la caché de la tarea, dentro de srt con
// el perfil de herramientas del perfil de código dado (con trustd: el Go encerrado sin él no verifica
// TLS y no descarga nada; probado). `go mod download` no ejecuta código de la rama. Es a mejor
// esfuerzo: si falla, el chequeo lo dice.
func bajarModulos(ctx context.Context, perfil string, wts ...string) {
	for _, wt := range wts {
		for _, mod := range modulosGo(wt) {
			cmd, err := comandoEncerrado(ctx, perfilHerramientasDe(perfil), "go mod download")
			if err != nil {
				return
			}
			enGrupo(cmd)
			cmd.Dir = mod
			cmd.Env = append(cleanEnv(), entornoDeTarea(dirTareaDe(wt))...)
			_ = cmd.Run()
			matarGrupo(cmd)
		}
	}
}

// modulosGo: las carpetas con go.mod del worktree (sin node_modules, vendor ni carpetas ocultas).
func modulosGo(wt string) []string {
	var mods []string
	_ = filepath.WalkDir(wt, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != wt && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || d.Name() == "vendor") {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == "go.mod" {
			mods = append(mods, filepath.Dir(p))
		}
		return nil
	})
	return mods
}
