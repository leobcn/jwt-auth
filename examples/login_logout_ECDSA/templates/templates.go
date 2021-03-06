package templates

import (
	"log"
	"net/http"
	"html/template"
)

type LoginPage struct {

}

type RestrictedPage struct {
	CsrfSecret string
	Role string
}

var templates = template.Must(template.ParseFiles("./templates/templateFiles/login.tmpl", "./templates/templateFiles/restricted.tmpl"))

func RenderTemplate(w http.ResponseWriter, tmpl string, p interface{}) {
	err := templates.ExecuteTemplate(w, tmpl+".tmpl", p)
	if err != nil {
		log.Printf("Temlate error here: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}