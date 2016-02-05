//
// This is a static file web server and search service.
// for interacting with ArchivesSpace's REST API
//
// @author R. S. Doiel, <rsdoiel@caltech.edu>
//
// Copyright (c) 2016, Caltech
// All rights not granted herein are expressly reserved by Caltech.
//
// Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:
//
// 1. Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.
//
// 2. Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.
//
// 3. Neither the name of the copyright holder nor the names of its contributors may be used to endorse or promote products derived from this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
//
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"../../../cait"
	"github.com/blevesearch/bleve"
)

var (
	description = `
 USAGE: caitserver [OPTIONS]

 OVERVIEW

	caitserver provides search services defined by CAIT_SITE_URL for the
	website content defined by CAIT_HTDOCS using the index defined
	by CAIT_BLEVE_INDEX.

 OPTIONS
`
	configuration = `
 CONFIGURATION

 caitserver can be configured through environment variables. The following
 variables are supported-

   CAIT_SITE_URL

   CAIT_BLEVE_INDEX

   CAIT_TEMPLATES

`
	help         bool
	indexName    string
	htdocsDir    string
	templatesDir string
	serviceURL   *url.URL

	advancedPage []byte
	basicPage    []byte

	index bleve.Index
)

func usage() {
	fmt.Println(description)
	flag.PrintDefaults()
	fmt.Println(configuration)
	os.Exit(0)
}

func mapToSearchQuery(m map[string]interface{}) (*cait.SearchQuery, error) {
	var err error
	q := new(cait.SearchQuery)
	src, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("Can't marshal %+v, %s", m, err)
	}
	err = json.Unmarshal(src, &q)
	if err != nil {
		return nil, fmt.Errorf("Can't unmarshal %s, %s", src, err)
	}
	return q, nil
}

func urlToRepoAccessionIDs(uri string) (int, int, error) {
	var err error
	repoID := 0
	accessionID := 0

	log.Printf("DEBUG uri in: %s\n", uri)
	parts := strings.SplitN(uri, "/", 7)
	log.Printf("DEBUG split path: %+v\n", parts)
	if len(parts) > 4 {
		repoID, err = strconv.Atoi(parts[4])
		if err != nil {
			return 0, 0, fmt.Errorf("Cannot parse repository id %s, %s", uri, err)
		}
	}
	if len(parts) >= 6 {
		id := filepath.Base(uri)
		accessionID, err = strconv.Atoi(id)
		if err != nil {
			return repoID, 0, fmt.Errorf("Cannot parse accession id %s, %s", uri, err)
		}
	}
	return repoID, accessionID, nil
}

func resultsHandler(w http.ResponseWriter, r *http.Request) {
	var (
		pageHTML    = "results-search.html"
		pageInclude = "results-search.include"
	)

	query := r.URL.Query()
	err := r.ParseForm()
	if err != nil {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(fmt.Sprintf("error in POST: %s", err)))
		return
	}

	submission := make(map[string]interface{})
	// Basic Search results
	if r.Method == "GET" {
		fmt.Printf("DEBUG GET submitted: %+v\n", query)
		for k, v := range query {
			if k == "all_ids" {
				b, _ := strconv.ParseBool(strings.Join(v, ""))
				submission[k] = b
			} else if k == "page" || k == "page_size" || k == "total" {
				i, _ := strconv.Atoi(strings.Join(v, ""))
				submission[k] = i
			} else {
				submission[k] = strings.Join(v, "")
			}
		}
	}
	// Advanced Search results
	if r.Method == "POST" {
		fmt.Printf("DEBUG POST submitted: %+v\n", r.Form)
		for k, v := range r.Form {
			if k == "all_ids" {
				b, _ := strconv.ParseBool(strings.Join(v, ""))
				submission[k] = b
			} else if k == "page" || k == "page_size" || k == "total" {
				i, _ := strconv.Atoi(strings.Join(v, ""))
				submission[k] = i
			} else {
				submission[k] = strings.Join(v, "")
			}
		}
	}

	q, err := mapToSearchQuery(submission)
	if err != nil {
		log.Printf("API access error %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf("%s", err)))
		return
	}
	q.DetailsBaseURI = "/search/results"

	// FIXME: Are we requesting a detailed result or a list result?
	if strings.HasPrefix(r.URL.Path, "/search/results/repositories/") == true {
		// FIXME: Handle carrying the search contents with the detailed page contents
		repoID, accessionID, err := urlToRepoAccessionIDs(r.URL.Path)
		if err != nil {
			log.Printf("Could not determine repo and accession IDs %s, %s", r.URL.Path, err)
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(fmt.Sprintf("%s", err)))
			return

		}
		src, err := ioutil.ReadFile(path.Join(htdocsDir, "repositories", fmt.Sprintf("%d", repoID), "accessions", fmt.Sprintf("%d.json", accessionID)))
		if err != nil {
			log.Printf("Can't decode /repositories/%d/accessions/%d.json, %s", repoID, accessionID, err)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf("%s", err)))
			return
		}
		err = json.Unmarshal(src, &q.DetailedResult)
		pageHTML = "details-search.html"
		pageInclude = "details-search.include"
	} else {
		// FIXME: Add logic to handle basic and advanced search...

		// q match
		// q_required match all
		// q_exact match phrase
		// q_excluded disjunct with match

		qry := bleve.NewMatchQuery(q.Q)
		search := bleve.NewSearchRequest(qry)
		search.Highlight = bleve.NewHighlightWithStyle("html")

		searchResults, err := index.Search(search)
		if err != nil {
			log.Printf("Bleve results error %v, %s", qry, err)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf("%s", err)))
			return
		}
		log.Printf("DEBUG searchResults: %+v\n", searchResults.Request.Query)
		src, _ := json.Marshal(searchResults.Request.Query)
		log.Printf("DEBUG src query: %s", src)
		queryTerms := struct {
			Match string `json:"match,omitempty"`
		}{}
		_ = json.Unmarshal(src, &queryTerms)
		log.Printf("DEBUG query terms [%s]\n", queryTerms.Match)

		// q (ciat.SearchQuery) performs double duty as both the structure for query submission as well
		// as carring the results to support paging and other types of navigation through
		// the query set. Results are a query with the bleve.SearchReults merged
		q.AttachSearchResults(searchResults)
		pageHTML = "results-search.html"
		pageInclude = "results-search.include"
	}

	// Load my tempaltes and setup to execute them
	tmpl, err := cait.AssembleTemplate(path.Join(templatesDir, pageHTML), path.Join(templatesDir, pageInclude))
	if err != nil {
		log.Printf("Template Errors: %s, %s, %s\n", pageHTML, pageInclude, err)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf("Template errors: %s", err)))
		return
	}
	// Render the page
	w.Header().Set("Content-Type", "text/html")
	err = tmpl.Execute(w, q)
	if err != nil {
		log.Printf("Can't render %s, %s/%s, %s", templatesDir, pageHTML, pageInclude, err)
		w.Write([]byte(fmt.Sprintf("Template error")))
	}
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	//logRequest(r)
	// If GET with Query String or POST pass to results handler
	// else display Basic Search Form
	query := r.URL.Query()
	if r.Method == "POST" || len(query) > 0 {
		resultsHandler(w, r)
		return
	}

	// Shared form data fields for a New Search.
	formData := struct {
		URI string
	}{
		URI: "/",
	}

	// Handle the basic or advanced search form requests.
	var (
		tmpl *template.Template
		err  error
	)
	w.Header().Set("Content-Type", "text/html")
	if strings.HasPrefix(r.URL.Path, "/search/advanced") == true {
		formData.URI = "/search/advanced/"
		tmpl, err = cait.AssembleTemplate(path.Join(templatesDir, "advanced-search.html"), path.Join(templatesDir, "advanced-search.include"))
		if err != nil {
			fmt.Printf("Can't read advanced-search templates, %s", err)
			return
		}
	} else {
		formData.URI = "/search/basic/"
		tmpl, err = cait.AssembleTemplate(path.Join(templatesDir, "basic-search.html"), path.Join(templatesDir, "basic-search.include"))
		if err != nil {
			log.Printf("Can't read basic-search templates, %s\n", err)
			return
		}
	}

	err = tmpl.Execute(w, formData)
	if err != nil {
		w.Write([]byte(fmt.Sprintf("%s", err)))
	}
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Request: %s Path: %s RemoteAddr: %s UserAgent: %s\n", r.Method, r.URL.Path, r.RemoteAddr, r.UserAgent())
		next.ServeHTTP(w, r)
	})
}

func searchRoutes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handler are searches and results
		if strings.HasPrefix(r.URL.Path, "/search/results/") == true {
			log.Printf("DEBUG results handler: %s", r.URL.Path)
			resultsHandler(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/search/") == true {
			log.Printf("DEBUG search handler: %s", r.URL.Path)
			searchHandler(w, r)
			return
		}
		// If it is not a search request send it on to the next handler...
		next.ServeHTTP(w, r)
	})
}

func init() {
	var err error

	uri := os.Getenv("CAIT_SITE_URL")
	indexName = os.Getenv("CAIT_BLEVE_INDEX")
	htdocsDir = os.Getenv("CAIT_HTDOCS")
	templatesDir = os.Getenv("CAIT_TEMPLATES")
	flag.StringVar(&uri, "search", "http://localhost:8501", "The URL to listen on for search requests")
	flag.StringVar(&indexName, "index", "index.bleve", "specify the Bleve index to use")
	flag.StringVar(&htdocsDir, "htdocs", "htdocs", "specify where to write the HTML files to")
	flag.StringVar(&templatesDir, "templates", "templates/default", "The directory path for templates")
	flag.BoolVar(&help, "h", false, "display this help message")
	flag.BoolVar(&help, "help", false, "display this help message")

	advancedPage, err = ioutil.ReadFile(path.Join(templatesDir, "advanced-search.html"))
	if err != nil {
		log.Fatalf("Can't read templates/advanced.html, %s", err)
	}
	basicPage, err = ioutil.ReadFile(path.Join(templatesDir, "basic-search.html"))
	if err != nil {
		log.Fatalf("Can't read templates/basic.html, %s", err)
	}

	if uri != "" {
		serviceURL, err = url.Parse(uri)
		if err != nil {
			log.Fatalf("Aspace Search URL not valid, %s, %s", uri, err)
		}
	}
}

func main() {
	var err error
	flag.Parse()
	if help == true {
		usage()
	}

	// Wake up our search engine
	index, err = bleve.Open(indexName)
	if err != nil {
		log.Fatalf("Can't open Bleve index %s, %s", indexName, err)
	}
	defer index.Close()

	// Send static file request to the default handler,
	// search routes are handled by middleware searchRoutes()
	http.Handle("/", http.FileServer(http.Dir(htdocsDir)))

	log.Printf("Listening on %s\n", serviceURL.String())
	err = http.ListenAndServe(serviceURL.Host, requestLogger(searchRoutes(http.DefaultServeMux)))
	if err != nil {
		log.Fatal(err)
	}
}