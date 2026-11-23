// factory es el orquestador de ticket-factory: encadena expertos aislados (`claude -p --agent`),
// una tarea a la vez, hasta dejarla en ESPERANDO_APROBACION o TRABADA. No es un LLM.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	ticketfactory "github.com/santtisosa/ticket-factory"
	"github.com/santtisosa/ticket-factory/internal/fab"
)

const (
	pollEvery   = 30 * time.Second
	ingestEvery = 10 * time.Minute
)

const usage = `uso:
  fabrica init                  escribe fabrica.yaml (desde fabrica.yaml.ejemplo) si no existe
  fabrica doctor                revisa dependencias, token y fabrica.yaml
  fabrica help                  esta ayuda
  fabrica add [--proyecto <nombre>] [--prioridad <n>] "<tarea>"   carga una tarea
  fabrica status                una fila por tarea
  fabrica ver <id>              el detalle: de dónde vino, eventos, archivos
  fabrica aceptar <id>          pasa una PROPUESTA a la cola
  fabrica descartar <id>        descarta una PROPUESTA
  fabrica continuar <id> "<respuestas>"   retoma una tarea cuya spec frenó
  fabrica reintentar <id>       devuelve una TRABADA a la cola
  fabrica cancelar <id>         descarta una tarea que no está corriendo
  fabrica mergeado <id>         una tarea ya integrada en el main local (proyecto sin GitHub) pasa a INTEGRADA
  fabrica desplegar <id> [--seco]  ejecuta el deploy de fabrica.yaml de una tarea integrada
  fabrica notificar <id> [--enviar] [--para <correo>] [--mensaje "<texto>"]
                                muestra la notificación de una tarea en producción y su destinatario;
                                --enviar la envía por notificacion.canal (smtp, comentario o exec), una sola vez
  fabrica prioridad <id> <n>    más alta, antes
  fabrica pausar | seguir       deja de tomar tareas nuevas / vuelve a tomarlas
  fabrica preview <id>|bajar    levanta la app de una tarea que espera la aprobación con la receta preview
                                del proyecto (una sola a la vez, en http://127.0.0.1:<puerto libre>) o la baja
  fabrica aprobar <id> [--seco]    publica lo aprobado, abre (o reutiliza) el PR y dispara el CI si el repo es
                                ci_manual; pide confirmación por el diálogo del sistema (macOS, zenity) o
                                escribiendo el id en la terminal
  fabrica prs                   lee CI y merge de las PRs abiertas; con merge.automatico integra las que
                                tienen merge.check_requerido en verde y no tocan archivos sensibles
  fabrica replay [prefijo]      re-decide las vueltas ya corridas con las reglas de hoy (sin agentes)
  fabrica metricas [prefijo]    costo por etapa, por tarea y motivos de freno (solo lectura)
  fabrica limpiar               saca los worktrees de las tareas cerradas
  fabrica run [--once]          procesa la cola, una tarea por proyecto a la vez; levanta antes los
                                servicios de prueba del proyecto (Docker)
  fabrica serve [--abrir]       run + ingesta + página en un proceso (modo servicio: contrib/)
  fabrica web [--abrir]         solo la página, en http://127.0.0.1:7171
  fabrica ingestar              lee una vez las fuentes de fabrica.yaml (github-issues, github-projects,
                                betterstack, exec); solo lectura
  fabrica probar-app [<proyecto>]  levanta la app del proyecto desde main con su receta preview, escanea /
                                y la baja (sin agentes); sin proyecto, usa el único que declara preview
  fabrica preguntar "<pregunta>"      le pregunta al Jefe (responde con citas verificadas)
  fabrica indice                regenera el índice del cerebro
  fabrica bibliotecario <id>    el Bibliotecario aprende de una tarea ya cerrada
  fabrica lint-cerebro          el Jefe busca contradicciones en el cerebro (solo informa)
  fabrica cerebro-sync [--seco] lleva lo aprendido al repo de conocimiento como rama fabrica/cerebro (con confirmación)`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	// Antes de NewConfig: no crean runs/ y doctor reporta un fabrica.yaml inválido en vez de cortar.
	switch os.Args[1] {
	case "help", "-h", "--help":
		fmt.Println(usage)
		return
	case "init", "doctor":
		if err := sinEspacio(os.Args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	cfg, err := fab.NewConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	if ok, err := mando(ctx, cfg, os.Args[1], os.Args[2:]); ok {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	switch os.Args[1] {
	case "run":
		once := len(os.Args) > 2 && os.Args[2] == "--once"
		if err := run(ctx, cfg, once); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "status":
		if err := status(cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "ingestar":
		srcs, err := fab.SourcesDelEspacio(cfg.Home)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fuentes:", err)
			os.Exit(1)
		}
		if len(srcs) == 0 {
			fmt.Println("no hay fuentes declaradas en fabrica.yaml")
		}
		for _, l := range fab.Ingest(ctx, cfg.Runs, srcs) {
			fmt.Println(l)
		}
	case "preguntar", "bibliotecario", "aprobar":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		if err := sideCommand(ctx, cfg, os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "indice", "lint-cerebro", "cerebro-sync":
		if err := sideCommand(ctx, cfg, os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "serve":
		// Un solo proceso para el servicio (launchd o systemd): la página en una goroutine y la línea en primer plano.
		go func() {
			if err := web(ctx, cfg, slices.Contains(os.Args[2:], "--abrir")); err != nil {
				fmt.Fprintln(os.Stderr, "página:", err)
			}
		}()
		if err := run(ctx, cfg, false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "web":
		if err := web(ctx, cfg, slices.Contains(os.Args[2:], "--abrir")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "probar-app":
		proyecto := ""
		if len(os.Args) > 2 {
			proyecto = os.Args[2]
		}
		if err := fab.ProbarApp(ctx, cfg, proyecto); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

func run(ctx context.Context, cfg fab.Config, once bool) error {
	// Un solo proceso de línea: adentro reparte una tarea por proyecto; dos procesos se pisarían.
	lock, err := os.OpenFile(filepath.Join(cfg.Runs, ".cache", "run.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("ya hay una fábrica corriendo (otra ventana o terminal); esta no arranca")
	}
	// run.pid le dice a la página que la fábrica está prendida.
	pidPath := filepath.Join(cfg.Runs, ".cache", "run.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprint(os.Getpid())), 0o644); err != nil {
		fmt.Println("no se pudo escribir run.pid (la página la va a ver apagada):", err)
	}
	defer os.Remove(pidPath)
	fab.ReconcilePreview(cfg.Runs) // una vista previa de una tarea que ya no espera la aprobación sobra
	if n, err := fab.RecuperarEnCurso(cfg.Runs); err != nil {
		return err
	} else if n > 0 {
		fmt.Printf("%d tarea(s) habían quedado a mitad: vuelven a la cola\n", n)
	}
	if !once {
		go ingestaPeriodica(ctx, cfg)        // aparte: una fuente lenta no frena la cola
		go despuesDelMerge(ctx, cfg)         // aparte: un deploy o la redacción de una notificación tardan minutos
		go fab.VencerPreviews(ctx, cfg.Runs) // la vista previa se apaga sola aunque la línea esté ocupada
	}
	// Una tarea por proyecto a la vez: las de proyectos distintos corren en paralelo.
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		ocupado = map[string]bool{}
		termino = make(chan struct{}, 1)
	)
	defer wg.Wait() // Ctrl-C: las que están corriendo vuelven a la cola antes de soltar el lock
	exe, inicio := binarioActual()
	for ctx.Err() == nil {
		// Binario nuevo (`go build`): deja de tomar tareas, espera a que terminen las que corren y se
		// reemplaza a sí mismo. Así un arreglo de la fábrica entra sin que el operador reinicie a mano.
		if !once && binarioCambio(exe, inicio) {
			mu.Lock()
			libre := len(ocupado) == 0
			mu.Unlock()
			if libre {
				fmt.Println("binario nuevo: reinicio automático")
				err := syscall.Exec(exe, os.Args, fab.EntornoConToken()) // los fds son CLOEXEC: el lock se suelta
				// Si llega acá, el exec falló (binario a medio escribir): sigue con el de antes.
				fmt.Println("no se pudo reiniciar con el binario nuevo; se sigue con el anterior:", err)
				_, inicio = binarioActual()
			}
			select {
			case <-ctx.Done():
			case <-termino:
			case <-time.After(pollEvery):
			}
			continue
		}
		if !once && cfg.LintDue() {
			if out, err := cfg.LintCerebro(ctx); err != nil {
				fmt.Println("lint del cerebro:", err)
			} else {
				fmt.Println("lint del cerebro: informe en", out)
			}
		}
		// ponytail: con la suscripción no hay costo por uso; el tope solo corre si se configura.
		// Con el margen de la llamada más cara (la del Builder): si no, entre "ya no entra una llamada"
		// y "pasó el tope" la tarea se re-encolaba y volvía a fallar en loop (31/10).
		if cfg.PasaTope(cfg.BudgetCallUSD * 2) {
			fmt.Printf("tope diario alcanzado (US$%.2f de %.2f): se sigue mañana\n", fab.SpentToday(cfg.Runs), cfg.BudgetDayUSD)
			select {
			case <-ctx.Done():
			case <-time.After(hastaMañana()):
			}
			continue
		}
		var t *fab.Task
		var carril string
		if !fab.Pausada(cfg.Runs) {
			mu.Lock()
			t, carril, err = fab.SiguienteLibre(cfg.Runs, ocupado)
			if t != nil {
				ocupado[carril] = true
			}
			mu.Unlock()
			if err != nil {
				return err
			}
		}
		if t != nil {
			fmt.Printf("%s procesando (%s): %s\n", t.Est.ID, carril, t.Est.Titulo)
			wg.Add(1)
			go func() {
				defer wg.Done()
				cfg.Process(ctx, t)
				// El Bibliotecario anota lo aprendido de una tarea que cerró, no de una que volvió a la cola.
				if ctx.Err() == nil && t.Est.Estado != fab.Pendiente {
					cfg.Librarian(ctx, t)
				}
				fmt.Printf("%s → %s %s (US$%.2f)\n", t.Est.ID, t.Est.Estado, t.Est.Motivo, t.Est.CostUSD)
				mu.Lock()
				delete(ocupado, carril)
				mu.Unlock()
				select {
				case termino <- struct{}{}:
				default:
				}
			}()
			if once {
				return nil // el defer espera a que termine
			}
			continue
		}
		if once {
			fmt.Println("no hay tareas pendientes")
			return nil
		}
		select {
		case <-ctx.Done():
		case <-termino: // se liberó un proyecto: mirar la cola enseguida
		case <-time.After(pollEvery):
		}
	}
	return nil
}

// binarioActual: la ruta del ejecutable y su fecha al arrancar.
func binarioActual() (string, time.Time) {
	exe, err := os.Executable()
	if err != nil {
		return "", time.Time{}
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return exe, time.Time{}
	}
	return exe, fi.ModTime()
}

// binarioCambio: hay un ejecutable más nuevo en la misma ruta (y no a medio escribir: tiene que tener tamaño).
func binarioCambio(exe string, inicio time.Time) bool {
	if exe == "" || inicio.IsZero() {
		return false
	}
	fi, err := os.Stat(exe)
	return err == nil && fi.Size() > 0 && fi.ModTime().After(inicio)
}

func ingestaPeriodica(ctx context.Context, cfg fab.Config) {
	for {
		// fabrica.yaml se relee en cada vuelta: un cambio en las fuentes no exige reiniciar.
		if srcs, err := fab.SourcesDelEspacio(cfg.Home); err != nil {
			fmt.Println("ingesta: fuentes:", err)
		} else {
			for _, l := range fab.Ingest(ctx, cfg.Runs, srcs) {
				fmt.Println("ingesta:", l)
			}
		}
		for _, l := range cfg.SeguirPRs(ctx) {
			fmt.Println("PR:", l)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(ingestEvery):
		}
	}
}

// despuesDelMerge despliega las apps sueltas mergeadas y redacta las notificaciones de lo que llegó a
// producción, en su propia vuelta: no frena la ingesta ni el seguimiento de las PRs.
func despuesDelMerge(ctx context.Context, cfg fab.Config) {
	for {
		for _, l := range cfg.DesplegarPendientes(ctx) {
			fmt.Println(l)
		}
		for _, l := range cfg.RedactarNotificacionesPendientes(ctx) {
			fmt.Println(l)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(ingestEvery):
		}
	}
}

func hastaMañana() time.Duration {
	y, m, d := time.Now().Date()
	return time.Until(time.Date(y, m, d+1, 0, 1, 0, 0, time.Local))
}

func status(cfg fab.Config) error {
	ts, err := fab.LoadTasks(cfg.Runs)
	if err != nil {
		return err
	}
	for _, t := range ts {
		e := t.Est
		fmt.Printf("%-18s %-15s %-12s v%d  US$%5.2f  %s\n", e.ID, e.Estado, e.Paso, e.Vuelta, e.CostUSD, e.Titulo)
		if e.Motivo != "" {
			fmt.Printf("%18s └ %s\n", "", e.Motivo)
		}
	}
	if cfg.BudgetDayUSD > 0 {
		fmt.Printf("\nhoy: US$%.2f de US$%.2f\n", fab.SpentToday(cfg.Runs), cfg.BudgetDayUSD)
	} else {
		fmt.Printf("\nhoy: US$%.2f (equivalente; sin tope)\n", fab.SpentToday(cfg.Runs))
	}
	if fab.Pausada(cfg.Runs) {
		fmt.Println("PAUSADA: no toma tareas nuevas hasta `fabrica seguir`")
	}
	return nil
}

// sinEspacio corre init y doctor en el espacio de trabajo (FABRICA_HOME o el directorio actual).
func sinEspacio(cmd string) error {
	dir := os.Getenv("FABRICA_HOME")
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return err
		}
	}
	if cmd == "init" {
		p, err := fab.Init(dir, ticketfactory.EjemploEspacio)
		if err != nil {
			return err
		}
		fmt.Println("escrito", p, "— ajustar proyectos y repos, y verificar con `fabrica doctor`")
		return nil
	}
	token, err := fab.TokenGitHubDefinido()
	if err != nil {
		return err
	}
	cs, ok := fab.Doctor(dir, fab.BuscarBinario, token)
	for _, c := range cs {
		marca := "ok   "
		switch {
		case !c.OK && c.Requerido:
			marca = "FALTA"
		case !c.OK:
			marca = "aviso"
		}
		fmt.Printf("%s %-22s %s\n", marca, c.Nombre, c.Detalle)
	}
	if !ok {
		return fmt.Errorf("doctor: falta algo requerido")
	}
	return nil
}
