package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/santtisosa/ticket-factory/internal/fab"
)

// mando atiende los comandos de la terminal que cambian el estado de una tarea. Devuelve false si
// el comando no es de acá.
func mando(ctx context.Context, cfg fab.Config, cmd string, args []string) (bool, error) {
	id := func() (string, error) {
		if len(args) < 1 {
			return "", fmt.Errorf("falta el id de la tarea\n%s", usage)
		}
		return args[0], nil
	}
	switch cmd {
	case "add":
		return true, agregar(cfg, args)
	case "continuar":
		if len(args) < 2 {
			return true, fmt.Errorf("uso: fabrica continuar <id> \"<respuestas>\"")
		}
		return true, hecho(fab.PedirContinuar(cfg.Runs, args[0], strings.Join(args[1:], " ")), args[0]+" → vuelve a la cola con tus respuestas")
	case "aceptar", "descartar":
		i, err := id()
		if err != nil {
			return true, err
		}
		if cmd == "aceptar" {
			return true, hecho(fab.Aceptar(cfg.Runs, i), i+" → "+fab.Pendiente)
		}
		return true, hecho(fab.SetEstado(cfg.Runs, i, fab.Propuesta, fab.Descartada, "descartada por el operador"), i+" → "+fab.Descartada)
	case "reintentar":
		i, err := id()
		if err != nil {
			return true, err
		}
		return true, hecho(fab.Reintentar(cfg.Runs, i), i+" → vuelve a la cola")
	case "mergeado":
		i, err := id()
		if err != nil {
			return true, err
		}
		return true, hecho(cfg.MergeadoLocal(context.Background(), i), i+" → mergeada en el main local")
	case "notificar":
		i, err := id()
		if err != nil {
			return true, err
		}
		var enviar bool
		var para, mensaje string
		for j := 1; j < len(args); j++ {
			switch args[j] {
			case "--enviar":
				enviar = true
			case "--para", "--mensaje":
				if j+1 >= len(args) {
					return true, fmt.Errorf("%s necesita un valor", args[j])
				}
				if args[j] == "--para" {
					para = args[j+1]
				} else {
					mensaje = args[j+1]
				}
				j++
			default:
				return true, fmt.Errorf("no entiendo %q\n%s", args[j], usage)
			}
		}
		lines, err := cfg.Notificar(ctx, i, enviar, para, mensaje)
		for _, l := range lines {
			fmt.Println(l)
		}
		return true, err
	case "desplegar":
		i, err := id()
		if err != nil {
			return true, err
		}
		lines, err := cfg.Desplegar(ctx, i, len(args) > 1 && args[1] == "--seco")
		for _, l := range lines {
			fmt.Println(l)
		}
		return true, err
	case "cancelar":
		i, err := id()
		if err != nil {
			return true, err
		}
		return true, hecho(fab.Cancelar(cfg.Runs, i), i+" → descartada")
	case "prioridad":
		if len(args) < 2 {
			return true, fmt.Errorf("uso: factory prioridad <id> <número>")
		}
		p, err := strconv.Atoi(args[1])
		if err != nil {
			return true, fmt.Errorf("la prioridad es un número entero: %w", err)
		}
		return true, hecho(fab.SetPrioridad(cfg.Runs, args[0], p), fmt.Sprintf("%s → prioridad %d", args[0], p))
	case "pausar":
		return true, hecho(fab.Pausar(cfg.Runs), "pausada: termina la tarea en curso y no toma otra hasta `fabrica seguir`")
	case "seguir":
		return true, hecho(fab.Seguir(cfg.Runs), "sigue tomando tareas")
	case "ver":
		i, err := id()
		if err != nil {
			return true, err
		}
		s, err := fab.Ver(cfg.Runs, i)
		if err == nil {
			fmt.Print(s)
			if p := fab.LoadPreview(cfg.Runs); p != nil && p.ID == i {
				fmt.Print("\n", p.Resumen())
			}
		}
		return true, err
	case "preview":
		i, err := id()
		if err != nil {
			return true, err
		}
		if i == "bajar" {
			fab.StopPreview(cfg.Runs)
			fmt.Println("vista previa bajada")
			return true, nil
		}
		p, err := cfg.PreviewDe(ctx, i)
		if err != nil {
			return true, err
		}
		fmt.Print(p.Resumen())
		return true, nil
	case "limpiar":
		hechos, err := cfg.Limpiar(ctx)
		for _, h := range hechos {
			fmt.Println("borrado", h)
		}
		return true, err
	case "prs":
		for _, l := range cfg.SeguirPRs(ctx) {
			fmt.Println(l)
		}
		return true, nil
	case "metricas":
		prefijo := ""
		if len(args) > 0 {
			prefijo = args[0]
		}
		m, err := fab.Metricas(cfg.Runs, prefijo)
		if err != nil {
			return true, err
		}
		fmt.Print(m)
		return true, nil
	case "replay":
		prefijo := ""
		if len(args) > 0 {
			prefijo = args[0]
		}
		vs, err := fab.ReplayTodo(cfg.Runs, prefijo)
		if err != nil {
			return true, err
		}
		fmt.Print(fab.TablaReplay(vs))
		return true, nil
	}
	return false, nil
}

func hecho(err error, msg string) error {
	if err == nil {
		fmt.Println(msg)
	}
	return err
}

// agregar: fabrica add [--proyecto <nombre>] [--prioridad <n>] "<tarea>".
func agregar(cfg fab.Config, args []string) error {
	cliente, prioridad, fijada := "", 0, false
	for len(args) >= 2 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--proyecto":
			if !fab.EsCliente(cfg.Home, args[1]) {
				return fmt.Errorf("el proyecto %q no está en fabrica.yaml", args[1])
			}
			cliente = args[1]
		case "--prioridad":
			p, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("la prioridad es un número entero: %w", err)
			}
			prioridad, fijada = p, true
		default:
			return fmt.Errorf("opción desconocida: %s\n%s", args[0], usage)
		}
		args = args[2:]
	}
	texto := strings.TrimSpace(strings.Join(args, " "))
	if texto == "" {
		return fmt.Errorf("falta la tarea\n%s", usage)
	}
	t, err := fab.AddTask(cfg.Runs, texto, "prompt", fab.Pendiente, cliente)
	if err != nil {
		return err
	}
	if !fijada {
		prioridad = fab.PrioridadDe("prompt", 0, fab.Sensible(texto))
	}
	if err := fab.SetPrioridad(cfg.Runs, t.Est.ID, prioridad); err != nil {
		return err
	}
	fmt.Println("cargada", t.Est.ID)
	return nil
}
