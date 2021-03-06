# jwt-auth
jwt auth middleware in goLang


## Quickstart
~~~ go
package main

import (
  "net/http"
  "log"
  "time"

  "github.com/adam-hanna/jwt-auth/jwt"
)

var restrictedRoute jwt.Auth

var restrictedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  w.Write([]byte("Welcome to the secret area!"))
})

var regularHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  w.Write([]byte("Hello, World!"))
})

func main() {
  authErr := jwt.New(&restrictedRoute, jwt.Options{
    SigningMethodString:   "RS256",
    PrivateKeyLocation:    "keys/app.rsa",     // `$ openssl genrsa -out app.rsa 2048`
    PublicKeyLocation:     "keys/app.rsa.pub", // `$ openssl rsa -in app.rsa -pubout > app.rsa.pub`
    RefreshTokenValidTime: 72 * time.Hour,
    AuthTokenValidTime:    15 * time.Minute,
    Debug:                 false,
    IsDevEnv:              true,
  })
  if authErr != nil {
    log.Println("Error initializing the JWT's!")
    log.Fatal(authErr)
  }

  http.HandleFunc("/", regularHandler)
  // this will never be available because we never issue tokens
  // see login_logout example for how to provide tokens
  http.Handle("/restricted", restrictedRoute.Handler(restrictedHandler))

  log.Println("Listening on localhost:3000")
  http.ListenAndServe("127.0.0.1:3000", nil)
}
~~~

## Goals
It is important to understand the objective of this auth architecture. It certainly is not an applicable design for all use cases. Please read and understand the goals, below, and make changes to your own workflow to suit your specific needs.

1. Protection of non-critical api's (e.g. not meant for financial, healthcare, gov't, etc. services)
2. Stateless
3. User sessions
4. XSS protection
5. CSRF protection
6. Web and/or mobile

## Basics
The design of this auth system is based around the three major components, listed below.

1. Short-lived (minutes) JWT Auth Token
2. Longer-lived (hours / days) JWT Refresh Token
3. CSRF secret string

### 1. Short-lived (minutes) JWT Auth Token
The short-lived jwt auth token allows the user to make stateless requests to protected api endpoints. It has an expiration time of 15 minutes by default and will be refreshed by the longer-lived refresh token.

### 2. Longer-lived (hours/days) JWT Refresh Token
This longer-lived token will be used to update the auth tokens. These tokens have a 72 hour expiration time by default which will be updated each time an auth token is refreshed.

These refresh tokens contain an id which can be revoked by an authorized client.

### 3. CSRF Secret String
A CSRF secret string will be provided to each client and will be identical the CSRF secret in the auth and refresh tokens and will change each time an auth token is refreshed. These secrets will live in an "X-CSRF-Token" response header. These secrets will be sent along with the auth and refresh tokens on each api request. 

When request are made to protected endpoint, this CSRF secret needs to be sent to the server either as a hidden form value with a name of "X-CSRF-Token", in the request header with the key of "X-CSRF-Token", or in the "Authorization" request header with a value of "Basic " + token. This secret will be checked against the secret provided in the auth token in order to prevent CSRF attacks.

## Cookies or Bearer Tokens?
This API is setup to either use cookies (default) or bearer tokens. To use bearer tokens, set the BearerTokens option equal to true in the config settings.

When using bearer tokens, you'll need to include the auth and refresh jwt's (along with your csrf secret) in each request. You can either include them as a form value or as data in the body of the request if Content-Type is application/json. The keys should be "Auth_Token" and "Refresh_Token", respectively. See the bearerTokens example for sample code of both.

Ideally, if using bearer tokens, they should be stored in a location that cannot be accessed with javascript. You want to be able to separate your csrf secret from your jwt's. If using web, I suggest using cookies. If using mobile, store these in a secure manner!

If you are using cookies, the auth and refresh jwt's will automatically be included. You only need to include the csrf token.

## API

### Create a new jwt middleware
~~~ go
var restrictedRoute jwt.Auth
~~~

### JWT middleware options
~~~ go
type Options struct {
  SigningMethodString   string // one of "HS256", "HS384", "HS512", "RS256", "RS384", "RS512", "ES256", "ES384", "ES512"
  PrivateKeyLocation    string // only for RSA and ECDSA signing methods; only required if VerifyOnlyServer is false
  PublicKeyLocation     string // only for RSA and ECDSA signing methods
  HMACKey               []byte // only for HMAC-SHA signing method
  VerifyOnlyServer      bool // false = server can verify and issue tokens (default); true = server can only verify tokens
  BearerTokens          bool // false = server uses cookies to transport jwts (default); true = server uses bearer tokens
  RefreshTokenValidTime time.Duration
  AuthTokenValidTime    time.Duration
  Debug                 bool // true = more logs are shown
  IsDevEnv:             bool // true = in development mode; this sets http cookies (if used) to insecure; false = production mode; this sets http cookies (if used) to secure
}
~~~

### ClaimsType
~~~ go
type ClaimsType struct {
  // Standard claims are the standard jwt claims from the ietf standard
  // https://tools.ietf.org/html/rfc7519
  jwt.StandardClaims
  Csrf               string
  CustomClaims       map[string]interface{}
}
~~~

You don't have to worry about any of this, except know that there is a "CustomClaims" map that allows you to set whatever you want. See "IssueTokenClaims" and "GrabTokenClaims", below, for more.

### Initialize new JWT middleware
~~~ go
authErr := jwt.New(&restrictedRoute, jwt.Options{
  SigningMethodString:   "RS256",
  PrivateKeyLocation:    "keys/app.rsa",     // `$ openssl genrsa -out app.rsa 2048`
  PublicKeyLocation:     "keys/app.rsa.pub", // `$ openssl rsa -in app.rsa -pubout > app.rsa.pub`
  RefreshTokenValidTime: 72 * time.Hour,
  AuthTokenValidTime:    15 * time.Minute,
  Debug:                 false,
  IsDevEnv:              true,
})
if authErr != nil {
  log.Println("Error initializing the JWT's!")
  log.Fatal(authErr)
}
~~~

### Handle a restricted route (see below for integration with popular frameworks)
~~~ go
// outside of main()
var restrictedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  csrfSecret := w.Header().Get("X-CSRF-Token")
  claims, err := restrictedRoute.GrabTokenClaims(w, r)
  log.Println(claims)
  
  if err != nil {
    http.Error(w, "Internal Server Error", 500)
  } else {
    templates.RenderTemplate(w, "restricted", &templates.RestrictedPage{ csrfSecret, claims.CustomClaims["Role"].(string) })
  }
})

func main() {
  ...
  http.Handle("/restricted", restrictedRoute.Handler(restrictedHandler))

  log.Println("Listening on localhost:3000")
  http.ListenAndServe("127.0.0.1:3000", nil)
}
~~~

### Issue new tokens and CSRF secret (for instance, when a user provides valid login credentials)
~~~ go
// in a handler func
claims := jwt.ClaimsType{}
claims.CustomClaims = make(map[string]interface{})
claims.CustomClaims["Role"] = "user"

err := restrictedRoute.IssueNewTokens(w, claims)
if err != nil {
  http.Error(w, "Internal Server Error", 500)
}

w.WriteHeader(http.StatusOK)
~~~

### Get a CSRF secret from a response
~~~ go
// in a handler func
// note: this works because if the middleware has made it this far, the JWT middleware has written a CSRF secret to the response writer (w)
csrfSecret := w.Header().Get("X-CSRF-Token")
~~~

### Get the expiration time of the refresh token, in Unix time
~~~ go
// in a handler func
// note: this works because if the middleware has made it this far, the JWT middleware has written this to the response writer (w)
// note: also, this won't be exact and may be a few milliseconds off from the token's actual expiry
refreshExpirationTime := w.Header().Get("Refresh-Expiry")
~~~

### Get the expiration time of the auth token, in Unix time
~~~ go
// in a handler func
// note: this works because if the middleware has made it this far, the JWT middleware has written this to the response writer (w)
// note: also, this won't be exact and may be a few milliseconds off from the token's actual expiry
authExpirationTime := w.Header().Get("Auth-Expiry")
~~~

### Get claims from a request
~~~ go
// in a handler func
claims, err := restrictedRoute.GrabTokenClaims(w, r)
log.Println(claims)
~~~

### Nullify auth and refresh tokens (for instance, when a user logs out)
~~~ go
// in a handler func
restrictedRoute.NullifyTokens(&w, r)
http.Redirect(w, r, "/login", 302)
~~~

### Token Id checker
A function used to check if a refresh token id has been revoked. You can either use a blacklist of revoked tokens, or a whitelist of allowed tokens. Your call. This function simply needs to return true if the token id has not been revoked. This function is run everytime an auth token is refreshed.
~~~go
var restrictedRoute jwt.Auth

// create a database of refresh tokens
// map key is the jti (json token identifier)
// the val doesn't represent anything but could be used to hold "valid", "revoked", etc.
// in the real world, you would store these in your db. This is just an example.
var refreshTokens map[string]string

restrictedRoute.SetCheckTokenIdFunction(CheckRefreshToken)

func CheckRefreshToken(jti string) bool {
  return refreshTokens[jti] != ""
}
~~~

### Token Id revoker
A function that adds a token id to a blacklist of revoked tokens, or revokes it from a whitelist of allowed tokens (however you'd like to do it).
~~~go
var restrictedRoute jwt.Auth

// create a database of refresh tokens
// map key is the jti (json token identifier)
// the val doesn't represent anything but could be used to hold "valid", "revoked", etc.
// in the real world, you would store these in your db. This is just an example.
var refreshTokens map[string]string

restrictedRoute.SetRevokeTokenFunction(DeleteRefreshToken)

func DeleteRefreshToken(jti string) error {
  delete(refreshTokens, jti)
  return nil
}
~~~

### 500 error handling
Set the response to a 500 error.
~~~go
var restrictedRoute jwt.Auth

restrictedRoute.SetErrorHandler(myErrorHandler)

var myErrorHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  http.Error(w, "I pitty the fool who has a 500 internal server error", 500)
})
~~~

### 401 unauthorized handling
Set the response to a 401 unauthorized request
~~~go
var restrictedRoute jwt.Auth

restrictedRoute.SetUnauthorizedHandler(MyUnauthorizedHandler)

var MyUnauthorizedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  http.Error(w, "I pitty the fool who is unauthorized", 401)
})
~~~


## Integration with popular goLang web Frameworks (untested)

The architecture of this package was inspired by [Secure](https://github.com/unrolled/secure), so I believe the integrations, below, should work. But they are untested.

### [Echo](https://github.com/labstack/echo)
~~~ go
// main.go
package main

import (
  "net/http"
  "log"

  "github.com/labstack/echo"
  "github.com/adam-hanna/jwt-auth/jwt"
)

var restrictedRoute jwt.Auth

func main() {
  authErr := jwt.New(&restrictedRoute, jwt.Options{
    SigningMethodString:   "RS256",
    PrivateKeyLocation:    "keys/app.rsa",     // `$ openssl genrsa -out app.rsa 2048`
    PublicKeyLocation:     "keys/app.rsa.pub", // `$ openssl rsa -in app.rsa -pubout > app.rsa.pub`
    RefreshTokenValidTime: 72 * time.Hour,
    AuthTokenValidTime:    15 * time.Minute,
    Debug:                 false,
    IsDevEnv:              true,
  })
  if authErr != nil {
    log.Println("Error initializing the JWT's!")
    log.Fatal(authErr)
  }

  e := echo.New()

  e.Get("/", func(c *echo.Context) error {
    c.String(http.StatusOK, "Restricted")
    return nil
  })
  e.Use(restrictedRoute.Handler)

  e.Run("127.0.0.1:3000")
}
~~~

### [Gin](https://github.com/gin-gonic/gin)
~~~ go
// main.go
package main

import (
  "log"

  "github.com/gin-gonic/gin"
  "github.com/adam-hanna/jwt-auth/jwt"
)

var restrictedRoute jwt.Auth

func main() {
  authErr := jwt.New(&restrictedRoute, jwt.Options{
    SigningMethodString:   "RS256",
    PrivateKeyLocation:    "keys/app.rsa",     // `$ openssl genrsa -out app.rsa 2048`
    PublicKeyLocation:     "keys/app.rsa.pub", // `$ openssl rsa -in app.rsa -pubout > app.rsa.pub`
    RefreshTokenValidTime: 72 * time.Hour,
    AuthTokenValidTime:    15 * time.Minute,
    Debug:                 false,
    IsDevEnv:              true,
  })
  if authErr != nil {
    log.Println("Error initializing the JWT's!")
    log.Fatal(authErr)
  }
  restrictedFunc := func() gin.HandlerFunc {
    return func(c *gin.Context) {
      err := restrictedRoute.Process(c.Writer, c.Request)

      // If there was an error, do not continue.
      if err != nil {
        return
      }

      c.Next()
    }
  }()

  router := gin.Default()
  router.Use(restrictedFunc)

  router.GET("/", func(c *gin.Context) {
    c.String(200, "Restricted")
  })

  router.Run("127.0.0.1:3000")
}
~~~

### [Goji](https://github.com/zenazn/goji)
~~~ go
// main.go
package main

import (
  "net/http"
  "log"

  "github.com/adam-hanna/jwt-auth/jwt"
  "github.com/zenazn/goji"
  "github.com/zenazn/goji/web"
)

var restrictedRoute jwt.Auth

func main() {
  authErr := jwt.New(&restrictedRoute, jwt.Options{
    SigningMethodString:   "RS256",
    PrivateKeyLocation:    "keys/app.rsa",     // `$ openssl genrsa -out app.rsa 2048`
    PublicKeyLocation:     "keys/app.rsa.pub", // `$ openssl rsa -in app.rsa -pubout > app.rsa.pub`
    RefreshTokenValidTime: 72 * time.Hour,
    AuthTokenValidTime:    15 * time.Minute,
    Debug:                 false,
    IsDevEnv:              true,
  })
  if authErr != nil {
    log.Println("Error initializing the JWT's!")
    log.Fatal(authErr)
  }

  goji.Get("/", func(c web.C, w http.ResponseWriter, req *http.Request) {
    w.Write([]byte("Restricted"))
  })
  goji.Use(restrictedRoute.Handler)
  goji.Serve() // Defaults to ":8000".
}
~~~

### [Iris](https://github.com/kataras/iris)
~~~ go
//main.go
package main

import (
  "log"

  "github.com/kataras/iris"
  "github.com/adam-hanna/jwt-auth/jwt"
)

var restrictedRoute jwt.Auth

func main() {
  authErr := jwt.New(&restrictedRoute, jwt.Options{
    SigningMethodString:   "RS256",
    PrivateKeyLocation:    "keys/app.rsa",     // `$ openssl genrsa -out app.rsa 2048`
    PublicKeyLocation:     "keys/app.rsa.pub", // `$ openssl rsa -in app.rsa -pubout > app.rsa.pub`
    RefreshTokenValidTime: 72 * time.Hour,
    AuthTokenValidTime:    15 * time.Minute,
    Debug:                 false,
    IsDevEnv:              true,
  })
  if authErr != nil {
    log.Println("Error initializing the JWT's!")
    log.Fatal(authErr)
  }

  iris.UseFunc(func(c *iris.Context) {
    err := restrictedRoute.Process(c.ResponseWriter, c.Request)

    // If there was an error, do not continue.
    if err != nil {
      return
    }

    c.Next()
  })

  iris.Get("/home", func(c *iris.Context) {
    c.SendStatus(200,"Restricted")
  })

  iris.Listen(":8080")

}

~~~~

### [Negroni](https://github.com/codegangsta/negroni)
Note this implementation has a special helper function called `HandlerFuncWithNext`.
~~~ go
// main.go
package main

import (
  "net/http"
  "log"

  "github.com/codegangsta/negroni"
  "github.com/adam-hanna/jwt-auth/jwt"
)

var restrictedRoute jwt.Auth

func main() {
  mux := http.NewServeMux()
  mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
    w.Write([]byte("Restricted"))
  })

  authErr := jwt.New(&restrictedRoute, jwt.Options{
    SigningMethodString:   "RS256",
    PrivateKeyLocation:    "keys/app.rsa",     // `$ openssl genrsa -out app.rsa 2048`
    PublicKeyLocation:     "keys/app.rsa.pub", // `$ openssl rsa -in app.rsa -pubout > app.rsa.pub`
    RefreshTokenValidTime: 72 * time.Hour,
    AuthTokenValidTime:    15 * time.Minute,
    Debug:                 false,
    IsDevEnv:              true,
  })
  if authErr != nil {
    log.Println("Error initializing the JWT's!")
    log.Fatal(authErr)
  }

  n := negroni.Classic()
  n.Use(negroni.HandlerFunc(restrictedRoute.HandlerFuncWithNext))
  n.UseHandler(mux)

  n.Run("127.0.0.1:3000")
}
~~~