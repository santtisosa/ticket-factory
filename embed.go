// Package ticketfactory expone los archivos de la raíz del repo que el binario lleva adentro.
package ticketfactory

import _ "embed"

// EjemploEspacio es fabrica.yaml.ejemplo: lo que escribe `fabrica init`.
//
//go:embed fabrica.yaml.ejemplo
var EjemploEspacio []byte
