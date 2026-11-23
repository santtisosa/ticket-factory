package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/santtisosa/ticket-factory/internal/fab"
)

// sideCommand: los comandos que no procesan la cola (el Jefe, el Bibliotecario, la aprobación).
func sideCommand(ctx context.Context, cfg fab.Config, cmd string, args []string) error {
	seco := slices.Contains(args, "--seco")
	switch cmd {
	case "preguntar":
		r, err := cfg.Ask(ctx, strings.Join(args, " "))
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
	case "indice":
		if err := cfg.EnsureBrain(ctx); err != nil {
			return err
		}
		out, err := cfg.BuildIndex()
		if err != nil {
			return err
		}
		fmt.Println("índice en", out)
	case "bibliotecario":
		ts, err := fab.LoadTasks(cfg.Runs)
		if err != nil {
			return err
		}
		for _, t := range ts {
			if t.Est.ID == args[0] {
				// A mano se fuerza: la línea aprende una sola vez por tarea (ver debeAprender).
				_ = os.Remove(filepath.Join(t.Dir, "05-bibliotecario.json"))
				cfg.Librarian(ctx, t)
				fmt.Println(t.Est.ID, "→ ver 05-bibliotecario.json y el log de la tarea")
				return nil
			}
		}
		return fmt.Errorf("no existe la tarea %s", args[0])
	case "lint-cerebro":
		out, err := cfg.LintCerebro(ctx)
		if err != nil {
			return err
		}
		fmt.Println("informe en", out)
	case "aprobar":
		lines, err := cfg.Aprobar(ctx, args[0], seco, false)
		for _, l := range lines {
			fmt.Println(map[bool]string{true: "(seco) ", false: "✓ "}[seco] + l)
		}
		return err
	case "cerebro-sync":
		line, err := cfg.CerebroSync(ctx, seco)
		fmt.Println(map[bool]string{true: "(seco) ", false: "✓ "}[seco] + line)
		return err
	}
	return nil
}
