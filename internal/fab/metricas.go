package fab

import (
	"fmt"
	"strings"
)

// Metricas: cuánto cuesta y cómo termina lo que hace la fábrica, desde factory.db. Las que importan
// (Demystifying evals, Cognition): costo por PR mergeada, cuánto se gasta en tareas que no llegan,
// y por qué frena cada una (de la fábrica, de la tarea o una decisión del operador).
func Metricas(runs, prefijo string) (string, error) {
	db, err := abrirDB(runs)
	if err != nil {
		return "", err
	}
	like := prefijo + "%"
	var b strings.Builder

	var n, mergeadas int
	var total, enMergeadas float64
	err = db.QueryRow(`SELECT count(*), coalesce(sum(costo_usd),0),
		coalesce(sum(CASE WHEN estado=? THEN 1 ELSE 0 END),0),
		coalesce(sum(CASE WHEN estado=? THEN costo_usd ELSE 0 END),0)
		FROM tasks WHERE id LIKE ?`, Integrada, Integrada, like).Scan(&n, &total, &mergeadas, &enMergeadas)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "%d tareas, US$ %.2f en total. Mergeadas: %d", n, total, mergeadas)
	if mergeadas > 0 {
		fmt.Fprintf(&b, " → US$ %.2f por PR mergeada (contando todo lo gastado)", total/float64(mergeadas))
	}
	fmt.Fprintf(&b, ".\nGastado en tareas que no llegaron a mergearse: US$ %.2f.\n\n", total-enMergeadas)

	b.WriteString("Por etapa:\n")
	rows, err := db.Query(`SELECT paso, sum(costo_usd), count(*) FROM events
		WHERE tarea LIKE ? AND costo_usd > 0 GROUP BY paso ORDER BY sum(costo_usd) DESC`, like)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var paso string
		var usd float64
		var k int
		if err := rows.Scan(&paso, &usd, &k); err != nil {
			rows.Close()
			return "", err
		}
		fmt.Fprintf(&b, "  %-18s US$ %7.2f  %3d llamadas\n", paso, usd, k)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	rows.Close()

	b.WriteString("\nFrenos (cada vez que una tarea quedó TRABADA):\n")
	frenos := map[string]int{}
	rows, err = db.Query(`SELECT mensaje FROM events WHERE tarea LIKE ? AND estado=?`, like, Trabada)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			rows.Close()
			return "", err
		}
		frenos[ClaseDeFreno(m)]++
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	rows.Close()
	for _, k := range []string{"pide una decisión", "vueltas sin pasar", "infraestructura", "otro"} {
		if frenos[k] > 0 {
			fmt.Fprintf(&b, "  %-20s %d\n", k, frenos[k])
		}
	}
	return b.String(), nil
}

// ClaseDeFreno agrupa el motivo de una traba: una decisión que falta (del operador o del cliente),
// vueltas que no convergen (código o reglas de la fábrica), o infraestructura.
func ClaseDeFreno(motivo string) string {
	switch {
	case strings.Contains(motivo, "Spec writer frenó") || strings.Contains(motivo, "continuar"):
		return "pide una decisión"
	case strings.Contains(motivo, "vueltas sin pasar"):
		return "vueltas sin pasar"
	case strings.Contains(motivo, "worktrees:") || strings.Contains(motivo, "docker") || strings.Contains(motivo, "cuota"):
		return "infraestructura"
	}
	return "otro"
}
