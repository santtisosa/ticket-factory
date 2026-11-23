#!/bin/bash
# PreToolUse del Bibliotecario: solo escribe donde se le permite dentro del cerebro
# ($FABRICA_CEREBRO_PERMITIDO, rutas separadas por ":"; un directorio termina en "/").
# Se resuelven los symlinks, así no se sale por la carpeta de enlaces. Exit 2 = bloqueado.
set -u
python3 -c '
import json, os, sys
d = json.load(sys.stdin)
if d.get("tool_name") not in ("Edit", "Write", "MultiEdit", "NotebookEdit"):
    sys.exit(0)
p = (d.get("tool_input") or {}).get("file_path") or ""
real = os.path.realpath(p)
for a in os.environ.get("FABRICA_CEREBRO_PERMITIDO", "").split(":"):
    if not a:
        continue
    ra = os.path.realpath(a) + ("/" if a.endswith("/") else "")
    if (a.endswith("/") and real.startswith(ra)) or real == ra:
        sys.exit(0)
print("BLOQUEADO: el Bibliotecario solo escribe en las rutas de conocimiento.escribibles: " + real, file=sys.stderr)
sys.exit(2)
'
