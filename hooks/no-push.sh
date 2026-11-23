#!/bin/bash
# PreToolUse de ticket-factory: la fábrica nunca pushea, abre PRs ni mergea. Eso pasa después del
# `fabrica aprobar` de la persona que aprueba, desde Go. Exit 2 = bloqueado. Es la tercera capa: el clon no tiene URL de
# push y los permisos niegan git push y gh.
set -u
CMD="$(python3 -c 'import json,sys; print((json.load(sys.stdin).get("tool_input") or {}).get("command") or "")' 2>/dev/null)"
# git con cualquier opción antes (git -C x push, git -c a=b push) y cualquier uso de gh (gh api POST
# abre una PR igual que gh pr create); los agentes no lo necesitan.
if printf '%s' "$CMD" | grep -Eq '(^|[^[:alnum:]_.-])git([[:space:]]+-[^[:space:]]+([[:space:]]+[^-[:space:]][^[:space:]]*)?)*[[:space:]]+push([[:space:]]|$)|(^|[^[:alnum:]_./-])gh([[:space:]]|$)'; then
  echo "BLOQUEADO: ticket-factory no pushea ni usa gh. Eso ocurre solo después de la aprobación humana (fabrica aprobar)." >&2
  exit 2
fi
exit 0
