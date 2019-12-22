package main

import (
	_ "file-service-go/server"
	"net/http"
)

func main() {
	http.ListenAndServe(":8080", nil)
}

