package main

import (
	"./templates"
	"github.com/adam-hanna/jwt-auth/jwt"

	"log"
	"net/http"
	"strings"
	"time"
)

var restrictedRoute jwt.Auth

var myUnauthorizedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "I Pitty the fool who is Unauthorized", 401)
})

var restrictedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	csrfSecret := w.Header().Get("X-CSRF-Token")
	claims, err := restrictedRoute.GrabTokenClaims(w, r)
	log.Println(claims)

	if err != nil {
		http.Error(w, "Internal Server Error", 500)
	} else {
		templates.RenderTemplate(w, "restricted", &templates.RestrictedPage{csrfSecret, claims.CustomClaims["Role"].(string)})
	}
})

var loginHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		templates.RenderTemplate(w, "login", &templates.LoginPage{})

	case "POST":
		r.ParseForm()

		if strings.Join(r.Form["username"], "") == "testUser" && strings.Join(r.Form["password"], "") == "testPassword" {
			claims := jwt.ClaimsType{}
			claims.CustomClaims = make(map[string]interface{})
			claims.CustomClaims["Role"] = "user"

			err := restrictedRoute.IssueNewTokens(w, claims)
			if err != nil {
				http.Error(w, "Internal Server Error", 500)
			}

			w.WriteHeader(http.StatusOK)

		} else {
			http.Error(w, "Unauthorized", 401)
		}

	default:
		http.Error(w, "Method Not Allowed", 405)
	}
})

var logoutHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		restrictedRoute.NullifyTokens(&w, r)
		http.Redirect(w, r, "/login", 302)

	default:
		http.Error(w, "Method Not Allowed", 405)
	}
})

func main() {
	authErr := jwt.New(&restrictedRoute, jwt.Options{
		SigningMethodString:   "HS256",
		HMACKey:               []byte("My super secret key!"),
		RefreshTokenValidTime: 72 * time.Hour,
		AuthTokenValidTime:    15 * time.Minute,
		Debug:                 true,
		IsDevEnv:              true,
	})
	if authErr != nil {
		log.Println("Error initializing the JWT's!")
		log.Fatal(authErr)
	}

	restrictedRoute.SetUnauthorizedHandler(myUnauthorizedHandler)

	http.HandleFunc("/", loginHandler)
	http.Handle("/restricted", restrictedRoute.Handler(restrictedHandler))
	http.Handle("/logout", restrictedRoute.Handler(logoutHandler))

	log.Println("Listening on localhost:3000")
	http.ListenAndServe("127.0.0.1:3000", nil)
}
