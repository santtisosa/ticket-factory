#!/bin/bash
# PreToolUse de ticket-factory para el MCP de Playwright del revisor de UX. El servidor corre fuera de
# todo sandbox: con `filename` (o `paths`) una herramienta escribiría o leería un archivo con la ruta
# que elija el agente, que puede estar inducido por la app de la rama (por ejemplo, plantar
# .claude/settings.local.json en una carpeta de la fábrica). Sin parámetros de archivo, lo que guarda va
# a --output-dir, dentro de la tarea. Exit 2 = bloqueado. Si no se puede leer la entrada, también.
set -u
python3 -c '
import json, sys
try:
    entrada = (json.load(sys.stdin).get("tool_input") or {})
except Exception:
    sys.exit(1)
sys.exit(1 if any(k in entrada for k in ("filename", "paths")) else 0)
' 2>/dev/null
case $? in
  0) exit 0 ;;
  *) echo "BLOQUEADO: el revisor de UX no guarda ni sube archivos con nombre propio; sin filename la captura va a la carpeta de la tarea." >&2
     exit 2 ;;
esac
