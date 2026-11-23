#!/bin/bash
# Stop del Builder: no lo deja terminar con el chequeo en rojo. Corre $FABRICA_CHECK_ENCERRADO, el
# chequeo de cada repo dentro de srt con el perfil de la tarea (lo arma el orquestador, encierro.go):
# los hooks no pasan por el sandbox de Claude Code y el código de la rama no corre sin encierro. Sin
# esa variable no corre nada; el chequeo de la fábrica igual decide. Una sola vez por turno: con
# stop_hook_active deja pasar, y el orquestador igual vuelve a correr el chequeo y lo cuenta.
set -u
INPUT="$(cat)"
printf '%s' "$INPUT" | grep -q '"stop_hook_active": *true' && exit 0
[ -n "${FABRICA_CHECK_ENCERRADO:-}" ] || exit 0
if ! OUT="$(bash -c "$FABRICA_CHECK_ENCERRADO" 2>&1)"; then
  printf 'El chequeo está en rojo. Hay que arreglarlo antes de terminar:\n%s\n' "$(printf '%s' "$OUT" | tail -40)" >&2
  exit 2
fi
exit 0
