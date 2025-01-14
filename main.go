package main

import (
	"context"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v2"
)

type RuleFile struct {
	Rules []Rule `yaml:"rules"`
}

type Rule map[string]interface{}

type PackFile struct {
	Packs map[string][]string `yaml:"packs"`
}

func main() {
	dir := flag.String("dir", "./patterns/", "directory to scan for yaml files")
	listen := flag.String("listen", ":8080", "address to listen on")
	packyml := flag.String("packs", "./patterns/packs.yml", "list of rule packs")

	flag.Parse()

	rules := make(map[string]Rule)

	filepath.Walk(*dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if filepath.Ext(path) != ".yml" {
			return nil
		}

		b, err := ioutil.ReadFile(path)
		if err != nil {
			log.Println("error reading", path, ":", err)
			return nil
		}

		var r RuleFile
		if err := yaml.Unmarshal(b, &r); err != nil {
			log.Println("error loading", path, ":", err)
			return nil
		}

		for _, rr := range r.Rules {
			rules[rr["id"].(string)] = rr
		}

		return nil
	})

	log.Println("loaded", len(rules), "rules")

	var packs PackFile

	if *packyml != "" {
		b, err := ioutil.ReadFile(*packyml)
		if err != nil {
			log.Fatalln("error reading", *packyml, ":", err)
		}

		if err := yaml.Unmarshal(b, &packs); err != nil {
			log.Fatalln("error loading", *packyml, ":", err)
		}

		// ensure all referenced rules exist
		for _, v := range packs.Packs {
			for _, r := range v {
				if _, ok := rules[r]; !ok {
					log.Printf("error: pack %q contained unknown rule %q", v, r)
				}
			}
		}
	}

	log.Println("loaded", len(packs.Packs), "packs")

	handler := rulesHandler{rules, packs.Packs}

	mux := http.NewServeMux()

	mux.HandleFunc("/r/", handler.HandleRule)
	mux.HandleFunc("/p/", handler.HandlePack)

	log.Println("listening on", *listen)

	httpServer := &http.Server{
		Addr:    *listen,
		Handler: mux,
	}

	done := make(chan os.Signal)

	go func() {
		signal.Notify(done, syscall.SIGTERM, syscall.SIGINT)
	}()

	go func() {
		err := httpServer.ListenAndServe()
		if err != nil {
			if err != http.ErrServerClosed {
				log.Fatalln("error:", err)
			}
		}
	}()

	<-done

	log.Println("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalln("error:", err)
	}

	log.Println("shut down")
}

type rulesHandler struct {
	rules map[string]Rule
	packs map[string][]string
}

// TODO(dgryski): These responses should really be precomputed and cached rather than computed on-the-fly

func (rh rulesHandler) HandleRule(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.RequestURI, "/r/")
	log.Println("rule", p)

	rule, ok := rh.rules[p]
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/yaml")

	y := yaml.NewEncoder(w)
	defer y.Close()
	y.Encode(RuleFile{Rules: []Rule{rule}})
}

func (rh rulesHandler) HandlePack(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.RequestURI, "/p/")
	log.Println("pack", p)

	rules, ok := rh.packs[p]
	if !ok {
		http.NotFound(w, r)
		return
	}
	log.Println(rules, p)
	var f RuleFile
	for _, rule := range rules {
		f.Rules = append(f.Rules, rh.rules[rule])
	}

	w.Header().Set("Content-Type", "text/yaml")

	y := yaml.NewEncoder(w)
	defer y.Close()
	y.Encode(f)
}
