package fab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

// Config son las rutas y topes de la fábrica.
type Config struct {
	Home           string // el espacio de trabajo: la carpeta de fabrica.yaml (FABRICA_HOME o el directorio actual)
	Factory        string // la carpeta de la fábrica: agents/, hooks/ y el binario
	Runs           string
	AgentsFile     string
	SettingsFile   string // revisores y Spec writer (los del Builder y el revisor del resultado son por tarea: settingsDeTarea)
	BiblioSettings string // + solo escribe en el cerebro
	BudgetDayUSD   float64
	BudgetCallUSD  float64
}

// Agentes: todos los expertos son de la fábrica (los globales de la casa no se tocan).
func (c Config) agentPaths() map[string]string {
	return map[string]string{
		"spec-writer":   filepath.Join(c.Factory, "agents", "spec-writer.md"),
		"builder":       filepath.Join(c.Factory, "agents", "builder.md"),
		"ux":            filepath.Join(c.Factory, "agents", "ux.md"),
		"ui":            filepath.Join(c.Factory, "agents", "ui.md"),
		"jefe":          filepath.Join(c.Factory, "agents", "jefe.md"),
		"bibliotecario": filepath.Join(c.Factory, "agents", "bibliotecario.md"),
		"seguridad":     filepath.Join(c.Factory, "agents", "seguridad.md"),
		"codigo":        filepath.Join(c.Factory, "agents", "codigo.md"),
		"resultado":     filepath.Join(c.Factory, "agents", "resultado.md"),
		"notificacion":  filepath.Join(c.Factory, "agents", "notificacion.md"),
	}
}

func envFloat(name string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil && v > 0 {
		return v
	}
	return def
}

// dirDeLaFabrica: la carpeta del binario (ahí están agents/ y hooks/). FABRICA_DIR la pisa, para
// correr con `go run` o desde otro lado.
func dirDeLaFabrica() (string, error) {
	if d := os.Getenv("FABRICA_DIR"); d != "" {
		return filepath.Abs(d)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("no se encuentra el binario de la fábrica: %w", err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe), nil
}

// NewConfig arma las rutas y regenera agents.json y los settings en runs/.cache.
func NewConfig() (Config, error) {
	if err := loadEnvFile(); err != nil {
		return Config{}, err
	}
	guardarTokenGitHub() // fuera del entorno: solo lo reciben los exec de GitHub (github.go)
	factory, err := dirDeLaFabrica()
	if err != nil {
		return Config{}, err
	}
	// El espacio de trabajo es la carpeta de fabrica.yaml: FABRICA_HOME o el directorio actual. Se
	// exporta para que un agente que corre `fabrica preguntar` desde su worktree use el mismo.
	home := os.Getenv("FABRICA_HOME")
	if home == "" {
		if home, err = os.Getwd(); err != nil {
			return Config{}, err
		}
	}
	if home, err = filepath.Abs(home); err != nil {
		return Config{}, err
	}
	if err := os.Setenv("FABRICA_HOME", home); err != nil {
		return Config{}, err
	}
	c := Config{
		Home:          home,
		Factory:       factory,
		BudgetDayUSD:  envFloat("FABRICA_TOPE_DIA_USD", 0), // 0: sin tope (se usa la suscripción); ver PasaTope
		BudgetCallUSD: envFloat("FABRICA_TOPE_LLAMADA_USD", 3),
	}
	if e, err := CargarEspacio(home); err == nil {
		if e.Limites.PorDiaUSD > 0 && os.Getenv("FABRICA_TOPE_DIA_USD") == "" {
			c.BudgetDayUSD = e.Limites.PorDiaUSD
		}
		if e.Limites.PorLlamadaUSD > 0 && os.Getenv("FABRICA_TOPE_LLAMADA_USD") == "" {
			c.BudgetCallUSD = e.Limites.PorLlamadaUSD
		}
	} else if !errors.Is(err, ErrSinEspacio) {
		return c, err // un fabrica.yaml inválido se dice al arrancar, no a mitad de una tarea
	}
	c.Runs = filepath.Join(home, "runs")
	raizClones, raizRuns = filepath.Join(home, "clones"), c.Runs // git solo sigue worktrees de estos clones
	cache := filepath.Join(c.Runs, ".cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return c, fmt.Errorf("crear runs/: %w", err)
	}
	c.AgentsFile = filepath.Join(cache, "agents.json")
	c.SettingsFile = filepath.Join(cache, "settings.json")
	c.BiblioSettings = filepath.Join(cache, "settings-bibliotecario.json")
	if _, err := c.prepararDirAgentes(); err != nil {
		return c, err
	}
	if err := LoadAgents(c.agentPaths(), c.AgentsFile); err != nil {
		return c, err
	}
	for path, kind := range map[string]string{c.SettingsFile: "", c.BiblioSettings: "biblio"} {
		if err := c.writeSettings(path, kind); err != nil {
			return c, err
		}
	}
	return c, nil
}

// versionPlaywrightMCP: la versión fija del servidor MCP de Playwright del revisor de UX.
const versionPlaywrightMCP = "0.0.83"

// mcpPlaywright: el --mcp-config del revisor de UX de una vuelta. El servidor corre afuera de todo
// sandbox, así que npx no puede resolverlo desde algo que el código de la rama escriba: la caché es
// propia (runs/.cache/npm-fabrica, que el encierro y los agentes no escriben) y el directorio de
// trabajo es esa carpeta, no el worktree (npx usa el paquete del proyecto local si coincide la versión,
// y Claude Code no fija el cwd de un servidor MCP). El navegador pide solo al origen de la app de la
// tarea (--allowed-origins; Playwright aclara que no es una frontera de seguridad ni afecta
// redirecciones) y lo que guarda sin nombre va a salida, no a la caché.
func mcpPlaywright(cache, origen, salida string) ([]byte, error) {
	dir := filepath.Join(cache, "npm-fabrica")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	linea := "cd " + shQuote(dir) + " && exec npx -y @playwright/mcp@" + versionPlaywrightMCP + " --headless --isolated" +
		" --allowed-origins " + shQuote(origen) + " --output-dir " + shQuote(salida)
	return json.Marshal(map[string]any{"mcpServers": map[string]any{"playwright": map[string]any{
		"command": "/bin/sh", "args": []string{"-c", linea}, "env": map[string]string{"npm_config_cache": dir},
	}}})
}

type hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}
type hookGroup struct {
	Matcher string `json:"matcher,omitempty"`
	Hooks   []hook `json:"hooks"`
}

// writeSettings: los expertos no cargan los settings del usuario ni los del proyecto
// (--setting-sources local), así que los guardias de producción se declaran acá, más el bloqueo de
// push, el modelo por defecto y la atribución vacía. Los de "builder", "resultado", "lector" (revisores
// y UX) y "spec" llevan el sandbox de una tarea y se escriben por tarea (settingsDeTarea); acá, sin
// tarea, no escriben en ningún clon.
func (c Config) writeSettings(path, kind string) error {
	return c.escribirSettings(path, kind, "", nil, nil)
}

// soloLectura: las carpetas que el sandbox de un lector lee y no escribe (sus --add-dir, que el sandbox
// de Claude Code deja escribir por defecto, y los clones).
func (c Config) escribirSettings(path, kind, dirTarea string, repos []Repo, soloLectura []string) error {
	cmd := func(p string) []hook { return []hook{{Type: "command", Command: p}} }
	h := map[string][]hookGroup{
		"PreToolUse": {
			{Matcher: "Bash", Hooks: cmd(filepath.Join(c.Factory, "hooks", "no-push.sh"))},
			// El MCP de Playwright corre afuera de todo sandbox: ninguna herramienta suya escribe un
			// archivo con el nombre que elija el agente (C2).
			{Matcher: "mcp__playwright__.*", Hooks: cmd(filepath.Join(c.Factory, "hooks", "playwright-sin-archivos.sh"))},
		},
	}
	switch kind {
	case "builder":
		h["Stop"] = []hookGroup{{Hooks: cmd(filepath.Join(c.Factory, "hooks", "check-on-stop.sh"))}}
	case "biblio":
		h["PreToolUse"] = append(h["PreToolUse"], hookGroup{Matcher: "Edit|Write|MultiEdit|NotebookEdit", Hooks: cmd(filepath.Join(c.Factory, "hooks", "solo-cerebro.sh"))})
	}
	clones := filepath.Join(c.Home, "clones")
	// Segunda capa del no-push: los permisos niegan push y gh aunque un comando esquive al hook.
	deny := []string{"Bash(git push:*)", "Bash(git -C:*)", "Bash(git -c:*)", "Bash(git config:*)", "Bash(git remote:*)", "Bash(gh:*)",
		// Los guardias y permisos del espacio y de la fábrica no los toca ningún experto (con
		// --setting-sources local, uno con cwd dentro de la fábrica podría cargar su settings.local).
		"Edit(/" + filepath.Join(c.Home, ".claude") + "/**)", "Edit(/" + filepath.Join(c.Factory, ".claude") + "/**)",
		"Edit(/" + filepath.Join(c.Factory, "hooks") + "/**)",
		// Ni el código de la fábrica (la línea se re-ejecuta sola con un binario nuevo) ni su CLI.
		"Edit(/" + filepath.Join(c.Factory, "cmd") + "/**)", "Edit(/" + filepath.Join(c.Factory, "internal") + "/**)",
		// Ni el binario (la línea se re-ejecuta sola si cambia), ni los hooks, la config y el commondir de
		// los clones: git los usa afuera del sandbox (revisiones de seguridad del 10/11 y del 22/11).
		"Edit(/" + filepath.Join(c.Factory, "fabrica") + ")",
		"Edit(/" + clones + "/*/.git/hooks/**)", "Edit(/" + clones + "/*/.git/config)", "Edit(/" + clones + "/*/.git/commondir)",
		// Ni reemplazos, grafts, alternates, atributos o refs empaquetadas (revisión del 23/11): con ellos lo
		// revisado no es lo que se sube.
		"Edit(/" + clones + "/*/.git/packed-refs)", "Edit(/" + clones + "/*/.git/shallow)", "Edit(/" + clones + "/*/.git/info/**)",
		"Edit(/" + clones + "/*/.git/objects/info/**)", "Edit(/" + clones + "/*/.git/refs/replace/**)",
		// Ni la config, los settings y la memoria de Claude Code del usuario.
		"Edit(/" + filepath.Join(homeUsuario(), ".claude") + "/**)",
		// Ni a qué gitdir y a qué clon apunta cada worktree: la fábrica corre git sobre ellos afuera.
		"Edit(/" + clones + "/*/.git/worktrees/*/commondir)",
		"Edit(/" + clones + "/*/.git/worktrees/*/gitdir)",
		"Edit(/" + clones + "/*/.git/worktrees/*/config.worktree)",
		"Edit(/" + filepath.Join(c.Runs, "*") + "/wt/*/.git)",
		// Ni lo que Claude Code carga de un directorio (C3): settings, hooks y CLAUDE.local.md.
		"Edit(/" + filepath.Join(c.Runs, "*") + "/wt/*/.claude/**)", "Edit(/" + filepath.Join(c.Runs, "*") + "/wt/*/CLAUDE.local.md)",
		"Edit(/" + c.dirAgentes() + "/**)",
		// Ni los archivos de la tarea (entrada, decisión, aprobado, base, PRs, settings y MCP de la
		// tarea): con ellos decide el merge automático y arrancan los agentes.
		"Edit(/" + filepath.Join(c.Runs, "*") + "/*.json)",
		// Ni los settings y perfiles de srt (runs/.cache) ni la configuración del espacio.
		"Edit(/" + filepath.Join(c.Runs, ".cache") + "/**)", "Edit(/" + filepath.Join(c.Home, ArchivoEspacio) + ")",
	}
	// Las herramientas del MCP de Playwright que ejecutan código, suben o escriben archivos (C2).
	for _, t := range playwrightNegadas {
		deny = append(deny, "mcp__playwright__"+t)
	}
	// Nadie más que el Builder escribe en el .git de los clones. Al Builder no se le niega entero: Claude
	// Code suma los deny de Edit al denyWrite de su sandbox y su `git commit` escribe objetos y refs ahí;
	// conserva los deny de hooks y config de arriba.
	if kind != "builder" {
		deny = append(deny, "Edit(/"+clones+"/*/.git/**)")
	}
	// De la CLI, solo consultar (ask al Jefe, ver, status): ningún subcomando que cambie algo.
	for _, sub := range []string{"aprobar", "cerebro-sync", "add", "continuar", "aceptar", "descartar", "reintentar",
		"cancelar", "prioridad", "pausar", "seguir", "preview", "limpiar", "run", "serve", "web", "ingestar",
		"bibliotecario", "probar-app", "replay", "desplegar", "notificar"} {
		deny = append(deny, "Bash(./fabrica "+sub+":*)", "Bash("+filepath.Join(c.Factory, "fabrica")+" "+sub+":*)")
	}
	// Ningún experto lee credenciales ni el llavero (Read también lo leía aunque no pudiera escribir), ni
	// los datos de otras aplicaciones y de Claude Code, ni el resto de ~/Documents (M3).
	for _, p := range append(c.credenciales(), c.lecturaNegadaAgentes()...) {
		deny = append(deny, "Read(/"+p+"/**)", "Read(/"+p+")")
	}
	permisos := map[string]any{"deny": deny}
	switch kind {
	case "builder":
		// Edit y Write no pasan por el sandbox: en dontAsk se niega todo lo que no esté en allow, y el
		// Builder solo tiene allow para sus worktrees (revisión del 23/11: con Edit libre escribía tools/,
		// agents/, ~/.gitconfig o runs/<otra tarea>). Ruta tal cual y resuelta: un allow pide las dos.
		permisos["allow"] = c.editDelBuilder(repos)
	case "lector", "spec":
		deny = append(deny, bashNegadoLectores...)
		permisos["deny"] = deny
	}
	settings := map[string]any{
		"hooks": h, "permissions": permisos,
		// Sin memoria automática: se escribe sin pedir permiso y todos los agentes comparten cwd, así que
		// lo que dejara uno (o un ticket que lo indujo) lo leería el siguiente.
		"autoMemoryEnabled": false,
		// Lo que antes llegaba de ~/.claude/settings.json y sí importa: el modelo por defecto (los que
		// no fijan model: en su frontmatter) y commits/PRs sin atribución a Claude.
		"model": "opus", "effortLevel": "high",
		"attribution": map[string]any{"commit": "", "pr": "", "sessionUrl": false},
	}
	switch kind {
	case "builder", "resultado", "lector", "spec":
		settings["sandbox"] = c.sandboxDeTarea(dirTarea, repos, kind, soloLectura)
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// settingsDeTarea escribe runs/<id>/settings-<kind>.json (protegido como los demás json de la tarea) con
// el sandbox de esa tarea: escribe solo en sus clones (el Builder), su temporal y sus cachés.
func (c Config) settingsDeTarea(dirTarea, kind string, repos []Repo, soloLectura ...string) (string, error) {
	if err := crearDirsDeTarea(dirTarea); err != nil {
		return "", err
	}
	p := filepath.Join(dirTarea, "settings-"+kind+".json")
	return p, c.escribirSettings(p, kind, dirTarea, repos, soloLectura)
}

// bashNegadoLectores: lo que ejecuta o escribe aunque empiece como un comando de lectura (revisión del
// 23/11: `git grep -O'sh -c ...'`, `rg --pre`, `sort -o`, `sed -n '1w f'`, `git log --output=f`, `uniq in
// out`, `git diff --no-index <archivo de afuera>`). Para buscar y leer están Grep y Read. Un deny gana a
// cualquier allow, también al de los comandos de lectura que Claude Code trae de fábrica.
var bashNegadoLectores = []string{"Bash(git grep:*)", "Bash(rg:*)", "Bash(sed:*)", "Bash(sort:*)", "Bash(uniq:*)",
	"Bash(git * --output*)", "Bash(git * -O*)", "Bash(git * --open-files-in-pager*)", "Bash(git * --no-index*)",
	"Bash(git * --ext-diff*)", "Bash(git * --textconv*)"}

// editDelBuilder: los allow de Edit (cubren Write y NotebookEdit) del Builder: solo sus worktrees.
func (c Config) editDelBuilder(repos []Repo) []string {
	var allow []string
	for _, r := range repos {
		if r.WT == "" {
			continue
		}
		for _, p := range []string{r.WT, realOIgual(r.WT)} {
			if regla := "Edit(/" + p + "/**)"; !slices.Contains(allow, regla) {
				allow = append(allow, regla)
			}
		}
	}
	return allow
}

// sandboxDeTarea: el sandbox de Claude Code de los agentes de una tarea. Los hooks y los deny son regex
// sobre el texto del comando (un script o un python -c los esquiva); el sandbox no.
//
//   - builder: el único con Bash libre. Escribe en sus worktrees (cwd y --add-dir, por defecto de Claude
//     Code), en los clones de su tarea (sus commits), en el temporal y las cachés de la tarea, y sale a la
//     red solo por la lista, sin github.com. Así no pushea, no recompila la fábrica ni toca otro repo.
//   - resultado: lo mismo sin escribir en los clones (no commitea).
//   - lector (revisores, UX) y spec: Bash solo con los comandos de su lista (sin autoAllow), sin red,
//     y sin escribir ni en sus --add-dir (soloLectura) ni en los clones: solo el temporal de la tarea.
//     El de spec corre `fabrica preguntar` afuera (excludedCommands): llama al Jefe, que necesita red.
//
// localhost queda abierto para el Builder y el resultado (testdb y next dev; en macOS es todo o nada).
// Docker no corre adentro y no se excluye (sería una salida). Los hooks, Edit y Write no pasan por este
// sandbox: el hook Stop corre el chequeo dentro de srt, y Edit se limita con allow y deny. Para todos,
// las credenciales, el llavero y el resto de ~/Documents no se leen (denyRead, con allowRead del
// espacio, de la fábrica y de lo que el lector lee), y nada escribe los archivos de git protegidos de
// los clones ni las refs y gitdirs de otras tareas (rutasGitProtegidas, rastrosDeOtrasTareas).
func (c Config) sandboxDeTarea(dirTarea string, repos []Repo, kind string, soloLectura []string) map[string]any {
	var escribe []string
	if dirTarea != "" {
		tmp, cache := dirsDeTarea(dirTarea)
		escribe = append(escribe, tmp, cache)
	}
	niega := []string{filepath.Join(c.Factory, "fabrica"), c.dirAgentes(), "/tmp/claude", "/private/tmp/claude"}
	lee := []string{c.Home, c.Factory, telemetriaGo()}
	lector := kind == "lector" || kind == "spec"
	for _, r := range repos {
		if r.Src == "" {
			continue
		}
		if kind == "builder" && !slices.Contains(escribe, r.Src) {
			escribe = append(escribe, r.Src)
		}
		niega = append(niega, rutasGitProtegidas(r.Src)...)
		if dirTarea != "" {
			niega = append(niega, rastrosDeOtrasTareas(r.Src, dirTarea)...)
		}
		if lector {
			niega = append(niega, r.Src)
			if r.WT != "" {
				niega = append(niega, r.WT)
			}
		}
	}
	if lector {
		niega = append(niega, soloLectura...)
		lee = append(lee, soloLectura...)
	}
	dominios := dominiosDependencias()
	if lector {
		dominios = []string{}
	}
	sb := map[string]any{
		"enabled": true, "failIfUnavailable": true,
		// Sin autoAllow, el Bash de un lector sigue pasando por su lista de comandos y los deny.
		"autoAllowBashIfSandboxed": !lector, "allowUnsandboxedCommands": false,
		// Explícitos aunque sean los valores por defecto (M4 y la salida por trustd): sin Apple Events y
		// sin el servicio de confianza TLS. Por lo segundo, el Go del sandbox no descarga módulos: los
		// baja la fábrica antes (bajarModulos).
		"allowAppleEvents": false, "enableWeakerNetworkIsolation": false,
		"filesystem": map[string]any{"allowWrite": escribe,
			"denyRead":  append(c.credenciales(), lecturaNegada()...),
			"allowRead": lee,
			// El cwd de los agentes es escribible por defecto en el sandbox de Claude Code: se niega.
			"denyWrite": conRutaReal(niega)},
		"network": map[string]any{
			"strictAllowlist": true, "allowLocalBinding": !lector,
			"allowedDomains": dominios,
		},
	}
	if kind == "spec" {
		sb["excludedCommands"] = []string{c.askCmd() + " *"}
	}
	return sb
}

// credenciales: lo que ningún experto ni el código de la rama lee (gcloud, las claves de la fábrica, gh,
// ssh, npm, el llavero de macOS). Van contra el home del usuario (os.UserHomeDir), no contra c.Home, que
// es el espacio de trabajo: contra c.Home apuntaría a <espacio>/.config y las credenciales reales
// quedarían leíbles. El llavero (C1): con él legible, `git credential fill` dentro de srt
// devolvía la contraseña de GitHub del usuario (probado: 1 línea password= sin el deny, 0 con él).
func (c Config) credenciales() []string {
	home := homeUsuario()
	var ps []string
	for _, p := range []string{".config/gcloud", ".config/ticket-factory", ".config/gh", ".ssh", ".aws", ".docker/config.json",
		".kube", ".git-credentials", ".netrc", ".npmrc", "Library/Keychains"} {
		ps = append(ps, filepath.Join(home, p))
	}
	return ps
}

// lecturaNegada: lo que el código encerrado y el Bash de los agentes no leen además de las credenciales
// (M3): ~/Documents (con allowRead de lo que sí necesitan), los datos de Claude Code, los de otras
// aplicaciones y las cookies.
func lecturaNegada() []string {
	h := homeUsuario()
	return []string{filepath.Join(h, "Documents"), filepath.Join(h, ".claude"),
		filepath.Join(h, "Library", "Application Support"), filepath.Join(h, "Library", "Cookies")}
}

// lecturaNegadaAgentes: lo mismo para la herramienta Read de los agentes, donde un deny no admite
// excepciones: de ~/Documents se niega cada entrada que no contiene al espacio ni a la fábrica, y de
// ~/.claude todo menos la carpeta de sesiones del cwd de los agentes (ahí deja Claude Code las salidas
// largas que el agente relee). Se calcula al escribir los settings: lo que aparezca después en esas
// carpetas queda leíble hasta la próxima vez.
func (c Config) lecturaNegadaAgentes() []string {
	h := homeUsuario()
	abiertos := []string{c.Home, c.Factory}
	for _, cl := range Clientes(c.Home) {
		for _, rc := range cl.Repos {
			if filepath.IsAbs(rc.Ruta) {
				abiertos = append(abiertos, rc.Ruta) // el Spec writer lee los repos de fabrica.yaml
			}
		}
	}
	ps := negarHermanos(filepath.Join(h, "Documents"), abiertos)
	proyecto := filepath.Join(h, ".claude", "projects", carpetaDeProyecto(c.dirAgentes()))
	ps = append(ps, negarHermanos(filepath.Join(h, ".claude"), []string{proyecto})...)
	return append(ps, filepath.Join(h, "Library", "Application Support"), filepath.Join(h, "Library", "Cookies"))
}

// negarHermanos: las entradas de raiz que no son ni contienen ninguna de abiertos (recorre hacia abajo
// hasta cada una). Sin raiz, nada.
func negarHermanos(raiz string, abiertos []string) []string {
	es, err := os.ReadDir(raiz)
	if err != nil {
		return nil
	}
	var ps []string
	for _, e := range es {
		p := filepath.Join(raiz, e.Name())
		abierto, contiene := false, false
		for _, a := range abiertos {
			switch {
			case mismaRuta(p, a):
				abierto = true
			case dentroDe(a, p):
				contiene = true
			}
		}
		switch {
		case abierto:
		case contiene && e.IsDir():
			ps = append(ps, negarHermanos(p, abiertos)...)
		default:
			ps = append(ps, p)
		}
	}
	return ps
}

// carpetaDeProyecto: el nombre que Claude Code le da en ~/.claude/projects a un cwd (cada carácter que
// no es letra ni número pasa a "-").
func carpetaDeProyecto(cwd string) string {
	b := []byte(cwd)
	for i, ch := range b {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

// dirAgentes: el cwd de todo `claude -p` (C3). Con --setting-sources local, Claude Code carga
// <cwd>/.claude/settings.local.json (y el de la raíz si el cwd está en un repo git), con hooks que -p
// ejecuta sin pedir confianza. Por eso el cwd es una carpeta propia de la fábrica, fuera de todo repo,
// que ni srt ni los agentes escriben (denyWrite y deny de Edit); los worktrees van solo por --add-dir.
// Vive en la caché del usuario, no bajo el espacio: el espacio puede ser un repo git.
func (c Config) dirAgentes() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	suma := sha256.Sum256([]byte(c.Home))
	return filepath.Join(base, "ticket-factory", "agentes-"+hex.EncodeToString(suma[:4]))
}

// prepararDirAgentes crea el cwd de los agentes y comprueba que sirva (cwdSeguro).
func (c Config) prepararDirAgentes() (string, error) {
	d := c.dirAgentes()
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, cwdSeguro(d)
}

// cwdSeguro: el cwd de un agente no está dentro de un repo git ni tiene nada que Claude Code cargue de
// un directorio. Lo comprueba Run antes de cada llamada.
func cwdSeguro(dir string) error {
	if dir == "" {
		return errors.New("seguridad: el agente no tiene cwd propio")
	}
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return fmt.Errorf("seguridad: el cwd de los agentes (%s) está dentro del repo git %s", dir, d)
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	for _, n := range []string{".claude", "CLAUDE.md", "CLAUDE.local.md", ".mcp.json"} {
		if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
			return fmt.Errorf("seguridad: alguien dejó %s en el cwd de los agentes (%s)", n, dir)
		}
	}
	return nil
}

// homeUsuario es el home de la cuenta (variable para los tests).
var homeUsuario = func() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "/nonexistent-home"
}

// PasaTope: gastar extra más lo de hoy pasa el tope diario. Con BudgetDayUSD en 0 no hay tope
// (se usa la suscripción). Todo control del tope pasa por acá.
func (c Config) PasaTope(extra float64) bool {
	return c.BudgetDayUSD > 0 && SpentToday(c.Runs)+extra > c.BudgetDayUSD
}
