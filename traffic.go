package main

// The bundled traffic analyzer, run from this binary:
//
//	mudi-screen app traffic serve          the collector (a service)
//	mudi-screen app traffic status         the view, for the screen
//	mudi-screen app traffic action ID ARG  a tap, for the screen
//
// status and action ask the collector over its unix socket (trafficd.go).

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

func runApp(args []string) int {
	if len(args) < 2 || args[0] != "traffic" {
		fmt.Fprintln(os.Stderr, "usage: mudi-screen app traffic serve | status | action ID [ARG...]")
		return 2
	}
	lang := os.Getenv("MUDI_SCREEN_LANG")
	switch args[1] {
	case "serve":
		return runTrafficd()
	case "status":
		return trafficRequest(tdRequest{Op: "view", Page: os.Getenv("MUDI_SCREEN_PAGE"), Lang: lang}, lang)
	case "action":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: mudi-screen app traffic action ID [ARG...]")
			return 2
		}
		return trafficRequest(tdRequest{Op: "action", ID: args[2], Args: args[3:], Lang: lang}, lang)
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n", args[1])
	return 2
}

func trafficRequest(req tdRequest, lang string) int {
	c, err := net.DialTimeout("unix", tdSockPath, 2*time.Second)
	if err != nil {
		if req.Op != "view" {
			fmt.Fprintln(os.Stderr, "the traffic analyzer is not running")
			return 1
		}
		// Without the collector the card says so instead of vanishing.
		var cat *catalog
		if dir := findAppDir("traffic"); dir != "" && lang != "" {
			cat = loadCatalog([]string{filepath.Join(dir, "lang", lang+".tsv")})
		}
		v := View{Card: &Card{Title: cat.tmpl("Traffic"), Subtitle: cat.tmpl("the analyzer is not running"), SubtitleColor: "orange",
			Icon: "chart", Color: "purple", Open: "main"},
			Pages: map[string]*Page{"main": {Title: cat.tmpl("Traffic"),
				Empty: cat.tmpl("The analyzer is not running: /etc/init.d/mudi-screen-traffic start")}}}
		_ = json.NewEncoder(os.Stdout).Encode(v)
		return 0
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(4 * time.Second))
	if err := json.NewEncoder(c).Encode(req); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if req.Op == "view" {
		if _, err := io.Copy(os.Stdout, c); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	var resp struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if resp.Error != "" {
		fmt.Fprintln(os.Stderr, resp.Error)
		return 1
	}
	if resp.Message != "" {
		fmt.Println(resp.Message)
	}
	return 0
}
