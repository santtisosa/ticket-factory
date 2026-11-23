package fab

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// La app de una rama se levanta con la receta `preview` del proyecto (fabrica.yaml). La usan la vista
// previa, los revisores de UX/UI y `fabrica probar-app`, cada uno en un puerto libre propio.

// appUpWait: cuánto se espera a que la app responda.
const appUpWait = 8 * time.Minute

// graciaBajar: después de SIGTERM, cuánto se espera antes de SIGKILL al grupo.
const graciaBajar = 5 * time.Second

// Variables para los tests.
var (
	esperaApp   = appUpWait
	puertoLibre = elegirPuerto
)

// Proceso es un grupo de procesos de la receta. Inicio es la hora de arranque del líder: antes de
// mandar una señal se compara, para no tocar otro programa que haya heredado el número.
type Proceso struct {
	PGID   int    `json:"pgid"`
	Inicio string `json:"inicio"`
}

// App es la aplicación levantada con la receta.
type App struct {
	URL      string
	Procesos []Proceso
}

// recetaDe devuelve el proyecto de esos repos y su receta de preview.
func (c Config) recetaDe(repos []string) (Cliente, *RecetaPreview, error) {
	if len(repos) == 0 {
		return Cliente{}, nil, fmt.Errorf("la tarea no nombra repos")
	}
	_, slug, ok := buscarRepo(Clientes(c.Home), repos[0])
	if !ok {
		return Cliente{}, nil, fmt.Errorf("el repo %q no es de ningún proyecto", repos[0])
	}
	return c.recetaDeProyecto(slug)
}

func (c Config) recetaDeProyecto(slug string) (Cliente, *RecetaPreview, error) {
	cl, ok := buscarCliente(Clientes(c.Home), slug)
	if !ok {
		return Cliente{}, nil, fmt.Errorf("el proyecto %q no existe en %s", slug, ArchivoEspacio)
	}
	e, err := CargarEspacio(c.Home)
	if err != nil {
		return Cliente{}, nil, err
	}
	receta := e.Proyectos[slug].Preview
	if receta == nil || len(receta.Comandos) == 0 {
		return Cliente{}, nil, fmt.Errorf("el proyecto %s no declara preview en %s", slug, ArchivoEspacio)
	}
	return cl, receta, nil
}

// repoApp: dónde corre la receta. El primer repo del proyecto con `frontend: true`; si no hay, el
// primero del proyecto. Los demás repos quedan como carpetas hermanas (../<repo>).
func repoApp(cl Cliente, repos []Repo) Repo {
	buscar := func(front bool) (Repo, bool) {
		for _, rc := range cl.Repos {
			if front && !rc.Front {
				continue
			}
			for _, r := range repos {
				if r.Name == rc.Nombre {
					return r, true
				}
			}
		}
		return Repo{}, false
	}
	if r, ok := buscar(true); ok {
		return r
	}
	if r, ok := buscar(false); ok {
		return r
	}
	return repos[0]
}

// arrancarApp corre los comandos de la receta en dir, en orden y sin esperar a que terminen (son
// servidores), con {puerto} reemplazado por uno libre de 127.0.0.1. Cada comando va en su propia
// sesión y no depende de ctx: la vista previa sobrevive al proceso que la lanzó. Un comando que
// termina con 0 se acepta (un paso previo o uno que deja procesos de fondo); uno que falla antes de
// que la app responda corta la espera, con el final del log en el motivo. Cada comando corre dentro de
// srt con el perfil de encierro (encierro.go): es código de la rama. Sin perfil o sin srt no arranca.
func arrancarApp(ctx context.Context, receta RecetaPreview, dir string, env []string, logPath, perfil string) (*App, error) {
	if _, _, err := argvEncerrado(perfil, "true"); err != nil {
		return nil, err
	}
	puerto, err := puertoLibre()
	if err != nil {
		return nil, fmt.Errorf("no hay un puerto libre en 127.0.0.1: %w", err)
	}
	f, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	a := &App{URL: "http://127.0.0.1:" + strconv.Itoa(puerto)}
	fallo := make(chan error, len(receta.Comandos))
	for _, linea := range receta.Comandos {
		linea = strings.ReplaceAll(linea, "{puerto}", strconv.Itoa(puerto))
		// El `wait` mantiene vivo al líder mientras viva cualquier proceso de fondo del comando: así el
		// grupo se reconoce por su líder y se baja entero. Sale con el estado del comando.
		cmd, err := comandoEncerrado(context.Background(), perfil, linea+"\nestado=$?\nwait\nexit $estado")
		if err != nil {
			a.Stop()
			return nil, err
		}
		cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, append(entornoLimpio(env...), entornoDeTarea(dirTareaDe(dir))...), f, f
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // sin la terminal de la línea; grupo = pid
		if err := cmd.Start(); err != nil {
			a.Stop()
			return nil, fmt.Errorf("%q: %w", linea, err)
		}
		a.Procesos = append(a.Procesos, Proceso{PGID: cmd.Process.Pid, Inicio: inicioDe(cmd.Process.Pid)})
		go func() { // sin zombis mientras viva quien lo lanzó
			if err := cmd.Wait(); err != nil {
				fallo <- fmt.Errorf("el comando %q terminó con error (%v)", linea, err)
			}
		}()
	}
	upCtx, cancel := context.WithTimeout(ctx, esperaApp)
	defer cancel()
	if err := esperarApp(upCtx, a.URL, fallo); err != nil {
		a.Stop()
		log, _ := os.ReadFile(logPath)
		return nil, fmt.Errorf("%w; final del log: %s", err, tail(string(log)))
	}
	return a, nil
}

// esperarApp espera a que la URL responda sin error del servidor, o a que un comando falle.
func esperarApp(ctx context.Context, url string, fallo <-chan error) error {
	for {
		select {
		case err := <-fallo:
			return err
		default:
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/", nil)
		if err != nil {
			return err
		}
		if res, err := http.DefaultClient.Do(req); err == nil {
			_ = res.Body.Close()
			if res.StatusCode < 500 {
				return nil
			}
		}
		select {
		case err := <-fallo:
			return err
		case <-ctx.Done():
			return fmt.Errorf("la app no respondió en %s dentro de %v", url, esperaApp)
		case <-time.After(time.Second):
		}
	}
}

// Stop baja todos los grupos de la receta.
func (a *App) Stop() { bajarProcesos(a.Procesos) }

// bajarProcesos: SIGTERM a cada grupo nuestro y, si sigue vivo tras la gracia, SIGKILL.
func bajarProcesos(ps []Proceso) {
	var vivos []int
	for _, p := range ps {
		if grupoNuestro(p) {
			_ = syscall.Kill(-p.PGID, syscall.SIGTERM)
			vivos = append(vivos, p.PGID)
		}
	}
	for limite := time.Now().Add(graciaBajar); len(vivos) > 0 && time.Now().Before(limite); time.Sleep(100 * time.Millisecond) {
		var quedan []int
		for _, pg := range vivos {
			if syscall.Kill(-pg, 0) == nil {
				quedan = append(quedan, pg)
			}
		}
		vivos = quedan
	}
	for _, pg := range vivos {
		_ = syscall.Kill(-pg, syscall.SIGKILL)
	}
}

// grupoNuestro: el líder del grupo sigue vivo y arrancó cuando lo lanzamos. Tras un reinicio el número
// puede ser de otro programa, y a ese no se le manda nada.
func grupoNuestro(p Proceso) bool {
	return p.PGID > 0 && p.Inicio != "" && inicioDe(p.PGID) == p.Inicio
}

// inicioDe: la hora de arranque del proceso según ps, o "" si no existe.
func inicioDe(pid int) string {
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(cleanEnv(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// elegirPuerto pide al sistema un puerto libre de 127.0.0.1.
// ponytail: se libera antes de que lo tome la receta; otro proceso podría ganarlo en ese instante.
func elegirPuerto() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// entornoLimpio: la app es código de la rama; no hereda los secretos de la fábrica. Lista blanca (la
// de los expertos) más las variables de los servicios. Sin Docker: dentro de srt no hay socket, y los
// servicios los levanta la fábrica desde fabrica.yaml.
func entornoLimpio(extra ...string) []string { return append(cleanEnv(), extra...) }

// usuarioDeApp: el usuario de prueba de la app, si está definido en el entorno de la fábrica.
func usuarioDeApp() (usuario, clave string) {
	return os.Getenv("FABRICA_APP_USUARIO"), os.Getenv("FABRICA_APP_CLAVE")
}

// Scan corre tools/ui-scan.mjs sobre las rutas de la spec, con el entorno limpio (sin los secretos de la
// fábrica). El usuario de prueba, si lo hay, le llega por el entorno (FABRICA_APP_USUARIO,
// FABRICA_APP_CLAVE), no por argumentos.
func (a *App) Scan(ctx context.Context, factory, outDir string, rutas []string) (string, error) {
	args := append([]string{filepath.Join(factory, "tools", "ui-scan.mjs"), a.URL, outDir}, rutas...)
	cmd := exec.CommandContext(ctx, "node", args...)
	cmd.Dir = filepath.Join(factory, "tools")
	u, k := usuarioDeApp()
	var extra []string
	if u != "" {
		extra = append(extra, "FABRICA_APP_USUARIO="+u, "FABRICA_APP_CLAVE="+k)
	}
	if l := os.Getenv("FABRICA_APP_LOGIN"); l != "" {
		extra = append(extra, "FABRICA_APP_LOGIN="+l)
	}
	cmd.Env = entornoLimpio(extra...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ui-scan: %w: %s", err, tail(string(out)))
	}
	return string(out), nil
}
