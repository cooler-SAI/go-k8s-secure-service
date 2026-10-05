package main

import (
	"fmt"
	"net/http"
)

func mainPage(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintln(w, "Hello, World!")
	if err != nil {
		fmt.Println("Error writing response:", err)
		return
	}
}

func livezHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintln(w, "OK")
	if err != nil {
		fmt.Println("Error writing response:", err)
		return
	}
}

func main() {
	http.HandleFunc("/", mainPage)
	http.HandleFunc("/livez", livezHandler)

	fmt.Println("Server starting on :8080...")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Println("Error starting server:", err)
		return
	}
}
