package main

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"flag"
	"github.com/tqrj/go-tdlib/internal/tlparser"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	var version string
	var outputPath string
	var schemaPath string
	var codePath string

	flag.StringVar(&version, "version", "", "TDLib version")
	flag.StringVar(&outputPath, "output", "./td_api.json", "json schema file")
	flag.StringVar(&schemaPath, "schema", "", "local td_api.tl (overrides download by -version)")
	flag.StringVar(&codePath, "code", "", "local Requests.cpp (overrides download by -version)")
	flag.Parse()

	schemaReader, closeSchema, err := openSource(schemaPath, "https://raw.githubusercontent.com/tdlib/td/"+version+"/td/generate/scheme/td_api.tl")
	if err != nil {
		log.Fatalf("open schema error: %s", err)
	}
	defer closeSchema()

	schema, err := tlparser.Parse(schemaReader)
	if err != nil {
		log.Fatalf("schema parse error: %s", err)
	}

	codeReader, closeCode, err := openSource(codePath, "https://raw.githubusercontent.com/tdlib/td/"+version+"/td/telegram/Requests.cpp")
	if err != nil {
		log.Fatalf("open code error: %s", err)
	}
	defer closeCode()

	err = tlparser.ParseCode(codeReader, schema)
	if err != nil {
		log.Fatalf("parse code error: %s", err)
	}

	err = os.MkdirAll(filepath.Dir(outputPath), os.ModePerm)
	if err != nil {
		log.Fatalf("make dir error: %s", filepath.Dir(outputPath))
	}

	f, err := os.Create(outputPath)
	if err != nil {
		log.Fatalf("open file error: %s", err)
	}
	defer f.Close()

	err = json.MarshalWrite(f, schema, jsontext.WithIndent("    "))
	if err != nil {
		log.Fatalf("json.MarshalWrite error: %s", err)
	}
}

// openSource returns a reader for a local file when path is set, otherwise
// downloads url. The schema can live in a TDLib fork that is not on GitHub
// under tdlib/td, so a local path must be an option.
func openSource(path, url string) (io.Reader, func(), error) {
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, err
		}
		return f, func() { f.Close() }, nil
	}
	res, err := http.Get(url)
	if err != nil {
		return nil, nil, err
	}
	return res.Body, func() { res.Body.Close() }, nil
}
