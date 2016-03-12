package middleware

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// This file contains a standard way for Caddy middleware
// to load files from the file system given a request
// URI and path to site root. Other middleware that load
// files should use these facilities.

// FileServer implements a production-ready file server
// and is the 'default' handler for all requests to Caddy.
// It simply loads and serves the URI requested. If Caddy is
// run without any extra configuration/directives, this is the
// only middleware handler that runs. It is not in its own
// folder like most other middleware handlers because it does
// not require a directive. It is a special case.
//
// FileServer is adapted from the one in net/http by
// the Go authors. Significant modifications have been made.
//
// Original license:
//
// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
func FileServer(root http.FileSystem, hide []string) Handler {
	return &fileHandler{root: root, hide: hide}
}

type fileHandler struct {
	root http.FileSystem
	hide []string // list of files to treat as "Not Found"
}

func (fh *fileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) (int, error) {
	upath := r.URL.Path
	if !strings.HasPrefix(upath, "/") {
		upath = "/" + upath
		r.URL.Path = upath
	}
	return fh.serveFile(w, r, path.Clean(upath))
}

// serveFile writes the specified file to the HTTP response.
// name is '/'-separated, not filepath.Separator.
func (fh *fileHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) (int, error) {
	// Prevent absolute path access on Windows.
	// TODO remove when stdlib http.Dir fixes this.
	if runtimeGoos == "windows" {
		if filepath.IsAbs(name[1:]) {
			return http.StatusNotFound, nil
		}
	}
	f, err := fh.root.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return http.StatusNotFound, nil
		} else if os.IsPermission(err) {
			return http.StatusForbidden, err
		}
		// Likely the server is under load and ran out of file descriptors
		w.Header().Set("Retry-After", "5") // TODO: 5 seconds enough delay? Or too much?
		return http.StatusServiceUnavailable, err
	}
	defer f.Close()

	d, err := f.Stat()
	if err != nil {
		if os.IsNotExist(err) {
			return http.StatusNotFound, nil
		} else if os.IsPermission(err) {
			return http.StatusForbidden, err
		}
		// Return a different status code than above so as to distinguish these cases
		return http.StatusInternalServerError, err
	}

	// redirect to canonical path
	url := r.URL.Path
	if d.IsDir() {
		// Ensure / at end of directory url
		if url[len(url)-1] != '/' {
			redirect(w, r, path.Base(url)+"/")
			return http.StatusMovedPermanently, nil
		}
	} else {
		// Ensure no / at end of file url
		if url[len(url)-1] == '/' {
			redirect(w, r, "../"+path.Base(url))
			return http.StatusMovedPermanently, nil
		}
	}

	// use contents of an index file, if present, for directory
	if d.IsDir() {
		for _, indexPage := range IndexPages {
			index := strings.TrimSuffix(name, "/") + "/" + indexPage
			ff, err := fh.root.Open(index)
			if err == nil {
				defer ff.Close()
				dd, err := ff.Stat()
				if err == nil {
					name = index
					d = dd
					f = ff
					break
				}
			}
		}
	}

	// Still a directory? (we didn't find an index file)
	// Return 404 to hide the fact that the folder exists
	if d.IsDir() {
		return http.StatusNotFound, nil
	}

	// If file is on hide list.
	if fh.isHidden(d) {
		return http.StatusNotFound, nil
	}

	// Note: Errors generated by ServeContent are written immediately
	// to the response. This usually only happens if seeking fails (rare).
	http.ServeContent(w, r, d.Name(), d.ModTime(), f)

	return http.StatusOK, nil
}

// isHidden checks if file with FileInfo d is on hide list.
func (fh fileHandler) isHidden(d os.FileInfo) bool {
	// If the file is supposed to be hidden, return a 404
	// (TODO: If the slice gets large, a set may be faster)
	for _, hiddenPath := range fh.hide {
		// Check if the served file is exactly the hidden file.
		if hFile, err := fh.root.Open(hiddenPath); err == nil {
			fs, _ := hFile.Stat()
			hFile.Close()
			if os.SameFile(d, fs) {
				return true
			}
		}
	}
	return false
}

// redirect is taken from http.localRedirect of the std lib. It
// sends an HTTP redirect to the client but will preserve the
// query string for the new path.
func redirect(w http.ResponseWriter, r *http.Request, newPath string) {
	if q := r.URL.RawQuery; q != "" {
		newPath += "?" + q
	}
	http.Redirect(w, r, newPath, http.StatusMovedPermanently)
}

// IndexPages is a list of pages that may be understood as
// the "index" files to directories.
var IndexPages = []string{
	"index.html",
	"index.htm",
	"index.txt",
	"default.html",
	"default.htm",
	"default.txt",
}
