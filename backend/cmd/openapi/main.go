// Command openapi dumps the huma-derived OpenAPI document deterministically
// (7.1). It builds the huma.API against handlers.NewForSpec() only, so it
// opens no database, dials no CA, and reads no environment variable.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"log"
	"os"

	humaapi "step-ui/api"
)

func main() {
	out := flag.String("out", "openapi/openapi.json", "output path")
	flag.Parse()

	spec := humaapi.NewForSpec()
	compact, err := spec.OpenAPI().MarshalJSON()
	if err != nil {
		log.Fatal(err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		log.Fatal(err)
	}
	pretty.WriteByte('\n')
	if err := os.WriteFile(*out, pretty.Bytes(), 0o644); err != nil { //nolint:gosec // G306: openapi.json is a committed source artifact, not sensitive
		log.Fatal(err)
	}
}
