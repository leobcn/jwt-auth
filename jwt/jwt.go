// much of the architecture for this package was taken from https://github.com/unrolled/secure
// thanks!

package jwt

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adam-hanna/randomstrings"
	jwtGo "github.com/dgrijalva/jwt-go"
)

type ClaimsType struct {
	// Standard claims are the standard jwt claims from the ietf standard
	// https://tools.ietf.org/html/rfc7519
	jwtGo.StandardClaims
	Csrf         string
	CustomClaims map[string]interface{}
}

// Options is a struct for specifying configuration options
type Options struct {
	SigningMethodString   string
	PrivateKeyLocation    string
	PublicKeyLocation     string
	HMACKey               []byte
	VerifyOnlyServer      bool
	BearerTokens          bool
	RefreshTokenValidTime time.Duration
	AuthTokenValidTime    time.Duration
	Debug                 bool
	IsDevEnv              bool
}

const defaultRefreshTokenValidTime = 72 * time.Hour
const defaultAuthTokenValidTime = 15 * time.Minute

func defaultTokenRevoker(tokenId string) error {
	return nil
}

type TokenRevoker func(tokenId string) error

func defaultCheckTokenId(tokenId string) bool {
	// return true if the token id is valid (has not been revoked). False for otherwise
	return true
}

type TokenIdChecker func(tokenId string) bool

func defaultErrorHandler(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Internal Server Error", 500)
	return
}

func defaultUnauthorizedHandler(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Unauthorized", 401)
	return
}

// this is a general json struct for when bearer tokens are used
type bearerTokensStruct struct {
	Auth_Token    string `json: "Auth_Token"`
	Refresh_Token string `json: "Refresh_Token"`
}

// Auth is a middleware that provides jwt based authentication.
type Auth struct {
	signKey   interface{}
	verifyKey interface{}

	options Options

	// Handlers for when an error occurs
	errorHandler        http.Handler
	unauthorizedHandler http.Handler

	// funcs for checking and revoking refresh tokens
	revokeRefreshToken TokenRevoker
	checkTokenId       TokenIdChecker
}

// New constructs a new Auth instance with supplied options.
func New(auth *Auth, options ...Options) error {
	var o Options
	if len(options) == 0 {
		o = Options{}
	} else {
		o = options[0]
	}

	// check if durations have been provided for auth and refresh token exp
	// if not, set them equal to the default
	if o.RefreshTokenValidTime <= 0 {
		o.RefreshTokenValidTime = defaultRefreshTokenValidTime
	}
	if o.AuthTokenValidTime <= 0 {
		o.AuthTokenValidTime = defaultAuthTokenValidTime
	}

	// create the sign and verify keys
	var signKey interface{}
	var verifyKey interface{}
	if o.SigningMethodString == "HS256" || o.SigningMethodString == "HS384" || o.SigningMethodString == "HS512" {
		if len(o.HMACKey) == 0 {
			return errors.New("When using an HMAC-SHA signing method, please provide a HMACKey")
		}
		if !o.VerifyOnlyServer {
			signKey = o.HMACKey
		}
		verifyKey = o.HMACKey

	} else if o.SigningMethodString == "RS256" || o.SigningMethodString == "RS384" || o.SigningMethodString == "RS512" {
		// check to make sure the provided options are valid
		if (o.PrivateKeyLocation == "" && !o.VerifyOnlyServer) || o.PublicKeyLocation == "" {
			return errors.New("Private and public key locations are required!")
		}

		// read the key files
		if !o.VerifyOnlyServer {
			signBytes, err := ioutil.ReadFile(o.PrivateKeyLocation)
			if err != nil {
				return err
			}

			signKey, err = jwtGo.ParseRSAPrivateKeyFromPEM(signBytes)
			if err != nil {
				return err
			}
		}

		verifyBytes, err := ioutil.ReadFile(o.PublicKeyLocation)
		if err != nil {
			return err
		}

		verifyKey, err = jwtGo.ParseRSAPublicKeyFromPEM(verifyBytes)
		if err != nil {
			return err
		}

	} else if o.SigningMethodString == "ES256" || o.SigningMethodString == "ES384" || o.SigningMethodString == "ES512" {
		// check to make sure the provided options are valid
		if (o.PrivateKeyLocation == "" && !o.VerifyOnlyServer) || o.PublicKeyLocation == "" {
			return errors.New("Private and public key locations are required!")
		}

		// read the key files
		if !o.VerifyOnlyServer {
			signBytes, err := ioutil.ReadFile(o.PrivateKeyLocation)
			if err != nil {
				return err
			}

			signKey, err = jwtGo.ParseECPrivateKeyFromPEM(signBytes)
			if err != nil {
				return err
			}
		}

		verifyBytes, err := ioutil.ReadFile(o.PublicKeyLocation)
		if err != nil {
			return err
		}

		verifyKey, err = jwtGo.ParseECPublicKeyFromPEM(verifyBytes)
		if err != nil {
			return err
		}

	} else {
		return errors.New("Signing method string not recognized!")
	}

	auth.signKey = signKey
	auth.verifyKey = verifyKey
	auth.options = o
	auth.errorHandler = http.HandlerFunc(defaultErrorHandler)
	auth.unauthorizedHandler = http.HandlerFunc(defaultUnauthorizedHandler)
	auth.revokeRefreshToken = TokenRevoker(defaultTokenRevoker)
	auth.checkTokenId = TokenIdChecker(defaultCheckTokenId)

	return nil
}

// add methods to allow the changing of default functions
func (a *Auth) SetErrorHandler(handler http.Handler) {
	a.errorHandler = handler
}
func (a *Auth) SetUnauthorizedHandler(handler http.Handler) {
	a.unauthorizedHandler = handler
}
func (a *Auth) SetRevokeTokenFunction(revoker TokenRevoker) {
	a.revokeRefreshToken = revoker
}
func (a *Auth) SetCheckTokenIdFunction(checker TokenIdChecker) {
	a.checkTokenId = checker
}

// Handler implements the http.HandlerFunc for integration with the standard net/http lib.
func (a *Auth) Handler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Process the request. If it returns an error,
		// that indicates the request should not continue.
		err := a.Process(w, r)

		// If there was an error, do not continue.
		if err != nil {
			return
		}

		h.ServeHTTP(w, r)
	})
}

// HandlerFuncWithNext is a special implementation for Negroni, but could be used elsewhere.
func (a *Auth) HandlerFuncWithNext(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	err := a.Process(w, r)

	// If there was an error, do not call next.
	if err == nil && next != nil {
		next(w, r)
	}
}

// Process runs the actual checks and returns an error if the middleware chain should stop.
func (a *Auth) Process(w http.ResponseWriter, r *http.Request) error {
	// cookies aren't included with options, so simply pass through
	if r.Method == "OPTIONS" {
		a.myLog("Method is OPTIONS")
		return nil
	}

	var authTokenValue string
	var refreshTokenValue string

	// read cookies
	if a.options.BearerTokens {
		// tokens are not in cookies
		if r.Header.Get("Content-Type") == "application/json" {
			content, err := ioutil.ReadAll(r.Body)
			if err != nil {
				a.myLog("Err decoding bearer tokens json \n" + err.Error())
				a.errorHandler.ServeHTTP(w, r)
				return errors.New("Internal Server Error")
			}
			r.Body = ioutil.NopCloser(bytes.NewReader(content))

			var bearerTokens bearerTokensStruct
			err = json.Unmarshal(content, &bearerTokens)
			if err != nil {
				a.myLog("Err decoding bearer tokens json \n" + err.Error())
				a.errorHandler.ServeHTTP(w, r)
				return errors.New("Internal Server Error")
			}
			authTokenValue = bearerTokens.Auth_Token
			refreshTokenValue = bearerTokens.Refresh_Token
		} else {
			r.ParseForm()
			authTokenValue = strings.Join(r.Form["Auth_Token"], "")
			refreshTokenValue = strings.Join(r.Form["Refresh_Token"], "")
		}
	} else {
		AuthCookie, authErr := r.Cookie("AuthToken")
		if authErr == http.ErrNoCookie {
			a.myLog("Unauthorized attempt! No auth cookie")
			a.NullifyTokens(&w, r)
			a.unauthorizedHandler.ServeHTTP(w, r)
			return errors.New("Unauthorized")
		} else if authErr != nil {
			a.myLog(authErr)
			a.NullifyTokens(&w, r)
			a.errorHandler.ServeHTTP(w, r)
			return errors.New("Internal Server Error")
		}
		authTokenValue = AuthCookie.Value

		RefreshCookie, refreshErr := r.Cookie("RefreshToken")
		if refreshErr == http.ErrNoCookie {
			a.myLog("Unauthorized attempt! No refresh cookie")
			a.NullifyTokens(&w, r)
			a.unauthorizedHandler.ServeHTTP(w, r)
			return errors.New("Unauthorized")
		} else if refreshErr != nil {
			a.myLog(refreshErr)
			a.NullifyTokens(&w, r)
			a.errorHandler.ServeHTTP(w, r)
			return errors.New("Internal Server Error")
		}
		refreshTokenValue = RefreshCookie.Value
	}

	// grab the csrf token
	requestCsrfToken := grabCsrfFromReq(r)

	// check the jwt's for validity
	authTokenString, refreshTokenString, csrfSecret, err := a.checkAndRefreshTokens(authTokenValue, refreshTokenValue, requestCsrfToken)
	if err != nil {
		if err.Error() == "Unauthorized" {
			a.myLog("Unauthorized attempt! JWT's not valid!")

			a.unauthorizedHandler.ServeHTTP(w, r)
			return errors.New("Unauthorized")
		} else if err.Error() == "Server is not authorized to issue new tokens" {
			a.unauthorizedHandler.ServeHTTP(w, r)
			return errors.New("Unauthorized")
		} else {
			// @adam-hanna: do we 401 or 500, here?
			// it could be 401 bc the token they provided was messed up
			// or it could be 500 bc there was some error on our end
			a.myLog(err)
			a.errorHandler.ServeHTTP(w, r)
			return errors.New("Internal Server Error")
		}
	}

	a.myLog("Successfully checked / refreshed jwts")

	// if we've made it this far, everything is valid!
	// And tokens have been refreshed if need-be
	a.setAuthAndRefreshTokens(&w, authTokenString, refreshTokenString)
	w.Header().Set("X-CSRF-Token", csrfSecret)
	w.Header().Set("Auth-Expiry", strconv.FormatInt(time.Now().Add(a.options.AuthTokenValidTime).Unix(), 10))
	w.Header().Set("Refresh-Expiry", strconv.FormatInt(time.Now().Add(a.options.RefreshTokenValidTime).Unix(), 10))

	return nil
}

// note @adam-hanna: this should return an error!
func (a *Auth) NullifyTokens(w *http.ResponseWriter, r *http.Request) {
	var refreshTokenValue string

	if a.options.BearerTokens {
		// tokens are not in cookies
		if r.Header.Get("Content-Type") == "application/json" {
			content, err := ioutil.ReadAll(r.Body)
			if err != nil {
				a.myLog("Err decoding bearer tokens json \n" + err.Error())
				a.errorHandler.ServeHTTP(*w, r)
				return
			}
			r.Body = ioutil.NopCloser(bytes.NewReader(content))

			var bearerTokens bearerTokensStruct
			err = json.Unmarshal(content, &bearerTokens)
			if err != nil {
				a.myLog("Err decoding bearer tokens json \n" + err.Error())
				a.errorHandler.ServeHTTP(*w, r)
				return
			}
			refreshTokenValue = bearerTokens.Refresh_Token
		} else {
			r.ParseForm()
			refreshTokenValue = strings.Join(r.Form["refresh_token"], "")
		}
	} else {
		authCookie := http.Cookie{
			Name:     "AuthToken",
			Value:    "",
			Expires:  time.Now().Add(-1000 * time.Hour),
			HttpOnly: true,
			Secure:   !a.options.IsDevEnv,
		}

		http.SetCookie(*w, &authCookie)

		refreshCookie := http.Cookie{
			Name:     "RefreshToken",
			Value:    "",
			Expires:  time.Now().Add(-1000 * time.Hour),
			HttpOnly: true,
			Secure:   !a.options.IsDevEnv,
		}

		http.SetCookie(*w, &refreshCookie)

		// if present, revoke the refresh cookie from our db
		RefreshCookie, refreshErr := r.Cookie("RefreshToken")
		if refreshErr == http.ErrNoCookie {
			// do nothing, there is no refresh cookie present
			return
		} else if refreshErr != nil {
			a.myLog(refreshErr)
			a.errorHandler.ServeHTTP(*w, r)
			return
		}
		refreshTokenValue = RefreshCookie.Value
	}

	a.revokeRefreshToken(refreshTokenValue)

	setHeader(*w, "X-CSRF-Token", "")
	setHeader(*w, "Auth-Expiry", strconv.FormatInt(time.Now().Add(-1000*time.Hour).Unix(), 10))
	setHeader(*w, "Refresh-Expiry", strconv.FormatInt(time.Now().Add(-1000*time.Hour).Unix(), 10))

	return
}

func (a *Auth) setAuthAndRefreshTokens(w *http.ResponseWriter, authTokenString string, refreshTokenString string) {
	if a.options.BearerTokens {
		// tokens are not in cookies
		setHeader(*w, "Auth_Token", authTokenString)
		setHeader(*w, "Refresh_Token", refreshTokenString)
	} else {
		// tokens are in cookies
		authCookie := http.Cookie{
			Name:     "AuthToken",
			Value:    authTokenString,
			Expires:  time.Now().Add(a.options.AuthTokenValidTime),
			HttpOnly: true,
			Secure:   !a.options.IsDevEnv,
		}
		http.SetCookie(*w, &authCookie)

		refreshCookie := http.Cookie{
			Name:     "RefreshToken",
			Value:    refreshTokenString,
			Expires:  time.Now().Add(a.options.RefreshTokenValidTime),
			HttpOnly: true,
			Secure:   !a.options.IsDevEnv,
		}
		http.SetCookie(*w, &refreshCookie)
	}
}

func grabCsrfFromReq(r *http.Request) string {
	csrfString := r.FormValue("X-CSRF-Token")

	if csrfString != "" {
		return csrfString
	}

	csrfString = r.Header.Get("X-CSRF-Token")
	if csrfString != "" {
		return csrfString
	}

	auth := r.Header.Get("Authorization")
	csrfString = strings.Replace(auth, "Basic", "", 1)
	return strings.Replace(csrfString, " ", "", -1)
}

// and also modify create refresh and auth token functions!
func (a *Auth) IssueNewTokens(w http.ResponseWriter, claims ClaimsType) error {
	if a.options.VerifyOnlyServer {
		a.myLog("Server is not authorized to issue new tokens")
		return errors.New("Server is not authorized to issue new tokens")

	} else {
		// generate the csrf secret
		csrfSecret, err := randomstrings.GenerateRandomString(32)
		if err != nil {
			return err
		}

		// generate the refresh token
		refreshTokenString, err := a.createRefreshTokenString(claims, csrfSecret)
		if err != nil {
			return err
		}

		// generate the auth token
		authTokenString, err := a.createAuthTokenString(claims, csrfSecret)
		if err != nil {
			return err
		}

		a.setAuthAndRefreshTokens(&w, authTokenString, refreshTokenString)

		w.Header().Set("X-CSRF-Token", csrfSecret)
		w.Header().Set("Auth-Expiry", strconv.FormatInt(time.Now().Add(a.options.AuthTokenValidTime).Unix(), 10))
		w.Header().Set("Refresh-Expiry", strconv.FormatInt(time.Now().Add(a.options.RefreshTokenValidTime).Unix(), 10))

		return nil
	}
}

// @adam-hanna: check if refreshToken["sub"] == authToken["sub"]?
// I don't think this is necessary bc a valid refresh token will always generate
// a valid auth token of the same "sub"
func (a *Auth) checkAndRefreshTokens(oldAuthTokenString string, oldRefreshTokenString string, oldCsrfSecret string) (newAuthTokenString, newRefreshTokenString, newCsrfSecret string, err error) {
	// first, check that a csrf token was provided
	if oldCsrfSecret == "" {
		a.myLog("No CSRF token in request!")
		err = errors.New("Unauthorized")
		return
	}

	// now, check that it matches what's in the auth token claims
	authToken, err := jwtGo.ParseWithClaims(oldAuthTokenString, &ClaimsType{}, func(token *jwtGo.Token) (interface{}, error) {
		if token.Method != jwtGo.GetSigningMethod(a.options.SigningMethodString) {
			a.myLog("Incorrect singing method on auth token")
			return nil, errors.New("Incorrect singing method on auth token")
		}
		return a.verifyKey, nil
	})

	authTokenClaims, ok := authToken.Claims.(*ClaimsType)
	if !ok {
		return
	}
	if oldCsrfSecret != authTokenClaims.Csrf {
		a.myLog("CSRF token doesn't match jwt!")
		err = errors.New("Unauthorized")
		return
	}

	// next, check the auth token in a stateless manner
	if authToken.Valid {
		a.myLog("Auth token is valid")
		// auth token has not expired
		// we need to return the csrf secret bc that's what the function calls for
		newCsrfSecret = authTokenClaims.Csrf

		// update the exp of refresh token string, but don't save to the db
		// we don't need to check if our refresh token is valid here
		// because we aren't renewing the auth token, the auth token is already valid
		if !a.options.VerifyOnlyServer {
			newRefreshTokenString, err = a.updateRefreshTokenExp(oldRefreshTokenString)
		} else {
			newRefreshTokenString = oldRefreshTokenString
		}
		newAuthTokenString = oldAuthTokenString
		return
	} else if ve, ok := err.(*jwtGo.ValidationError); ok {
		a.myLog("Auth token is not valid")
		if ve.Errors&(jwtGo.ValidationErrorExpired) != 0 {
			if a.options.VerifyOnlyServer {
				a.myLog("Server is not authorized to issue new tokens")
				err = errors.New("Server is not authorized to issue new tokens")
				return
			} else {
				a.myLog("Auth token is expired")
				// auth token is expired
				// fyi - refresh token is checked in the update auth func
				newAuthTokenString, newCsrfSecret, err = a.updateAuthTokenString(oldRefreshTokenString, oldAuthTokenString)
				if err != nil {
					return
				}

				// update the exp of refresh token string
				newRefreshTokenString, err = a.updateRefreshTokenExp(oldRefreshTokenString)
				if err != nil {
					return
				}

				// update the csrf string of the refresh token
				newRefreshTokenString, err = a.updateRefreshTokenCsrf(newRefreshTokenString, newCsrfSecret)
				return
			}
		} else {
			a.myLog("Error in auth token")
			err = errors.New("Error in auth token")
			return
		}
	} else {
		a.myLog("Error in auth token")
		err = errors.New("Error in auth token")
		return
	}
}

func (a *Auth) createRefreshTokenString(claims ClaimsType, csrfString string) (refreshTokenString string, err error) {
	refreshTokenExp := time.Now().Add(a.options.RefreshTokenValidTime).Unix()
	if err != nil {
		return
	}

	claims.StandardClaims.ExpiresAt = refreshTokenExp
	claims.Csrf = csrfString

	// create a signer
	refreshJwt := jwtGo.NewWithClaims(jwtGo.GetSigningMethod(a.options.SigningMethodString), claims)

	// generate the refresh token string
	refreshTokenString, err = refreshJwt.SignedString(a.signKey)
	return
}

func (a *Auth) createAuthTokenString(claims ClaimsType, csrfSecret string) (authTokenString string, err error) {
	authTokenExp := time.Now().Add(a.options.AuthTokenValidTime).Unix()

	claims.StandardClaims.ExpiresAt = authTokenExp
	claims.Csrf = csrfSecret

	// create a signer
	authJwt := jwtGo.NewWithClaims(jwtGo.GetSigningMethod(a.options.SigningMethodString), claims)

	// generate the auth token string
	authTokenString, err = authJwt.SignedString(a.signKey)
	return
}

func (a *Auth) updateRefreshTokenExp(oldRefreshTokenString string) (string, error) {
	refreshToken, _ := jwtGo.ParseWithClaims(oldRefreshTokenString, &ClaimsType{}, func(token *jwtGo.Token) (interface{}, error) {
		// no need verify refresh token alg because it was verified at `updateAuthTokenString`
		return a.verifyKey, nil
	})

	oldRefreshTokenClaims, ok := refreshToken.Claims.(*ClaimsType)
	if !ok {
		return "", errors.New("Error parsing claims")
	}

	refreshTokenExp := time.Now().Add(a.options.RefreshTokenValidTime).Unix()
	oldRefreshTokenClaims.StandardClaims.ExpiresAt = refreshTokenExp

	// create a signer
	refreshJwt := jwtGo.NewWithClaims(jwtGo.GetSigningMethod(a.options.SigningMethodString), oldRefreshTokenClaims)

	// generate the refresh token string
	return refreshJwt.SignedString(a.signKey)
}

func (a *Auth) updateAuthTokenString(refreshTokenString string, oldAuthTokenString string) (newAuthTokenString, csrfSecret string, err error) {
	refreshToken, err := jwtGo.ParseWithClaims(refreshTokenString, &ClaimsType{}, func(token *jwtGo.Token) (interface{}, error) {
		if token.Method != jwtGo.GetSigningMethod(a.options.SigningMethodString) {
			a.myLog("Incorrect singing method on auth token")
			return nil, errors.New("Incorrect singing method on auth token")
		}
		return a.verifyKey, nil
	})
	// if err != nil {
	// 	return
	// }

	refreshTokenClaims, ok := refreshToken.Claims.(*ClaimsType)
	if !ok {
		err = errors.New("Error reading jwt claims")
		return
	}

	// check if the refresh token has been revoked
	if a.checkTokenId(refreshTokenClaims.StandardClaims.Id) {
		a.myLog("Refresh token has not been revoked")
		// the refresh token has not been revoked
		// has it expired?
		if refreshToken.Valid {
			a.myLog("Refresh token is not expired")
			// nope, the refresh token has not expired
			// issue a new auth token

			// our policy is to regenerate the csrf secret for each new auth token
			csrfSecret, err = randomstrings.GenerateRandomString(32)
			if err != nil {
				return
			}

			newAuthTokenString, err = a.createAuthTokenString(*refreshTokenClaims, csrfSecret)

			// fyi - updating of refreshtoken csrf and exp is done after calling this func
			// so we can simply return
			return
		} else {
			a.myLog("Refresh token has expired!")
			// the refresh token has expired! Require the user to re-authenticate
			// @adam-hanna: Do we want to revoke the token in our db?
			// I don't think we need to because it has expired and we can simply check the
			// exp. No need to update the db.

			err = errors.New("Unauthorized")
			return
		}
	} else {
		a.myLog("Refresh token has been revoked!")
		// the refresh token has been revoked!
		err = errors.New("Unauthorized")
		return
	}
}

func (a *Auth) updateRefreshTokenCsrf(oldRefreshTokenString string, newCsrfString string) (string, error) {
	refreshToken, _ := jwtGo.ParseWithClaims(oldRefreshTokenString, &ClaimsType{}, func(token *jwtGo.Token) (interface{}, error) {
		// no need verify refresh token alg because it was verified at `updateAuthTokenString`
		return a.verifyKey, nil
	})

	oldRefreshTokenClaims, ok := refreshToken.Claims.(*ClaimsType)
	if !ok {
		return "", errors.New("Error parsing claims")
	}

	oldRefreshTokenClaims.Csrf = newCsrfString

	// create a signer
	refreshJwt := jwtGo.NewWithClaims(jwtGo.GetSigningMethod(a.options.SigningMethodString), oldRefreshTokenClaims)

	// generate the refresh token string
	return refreshJwt.SignedString(a.signKey)
}

func (a *Auth) GrabTokenClaims(w http.ResponseWriter, r *http.Request) (ClaimsType, error) {
	var authTokenValue string

	// read cookies
	if a.options.BearerTokens {
		// tokens are not in cookies
		if r.Header.Get("Content-Type") == "application/json" {
			content, err := ioutil.ReadAll(r.Body)
			if err != nil {
				a.myLog("Err decoding bearer tokens json \n" + err.Error())
				return ClaimsType{}, errors.New("Internal Server Error")
			}
			r.Body = ioutil.NopCloser(bytes.NewReader(content))

			var bearerTokens bearerTokensStruct
			err = json.Unmarshal(content, &bearerTokens)
			if err != nil {
				a.myLog("Err decoding bearer tokens json \n" + err.Error())
				return ClaimsType{}, errors.New("Internal Server Error")
			}
			authTokenValue = bearerTokens.Auth_Token
		} else {
			r.ParseForm()
			authTokenValue = strings.Join(r.Form["Auth_Token"], "")
		}
	} else {
		AuthCookie, authErr := r.Cookie("AuthToken")
		if authErr == http.ErrNoCookie {
			a.myLog("Unauthorized attempt! No auth cookie")
			a.NullifyTokens(&w, r)
			return ClaimsType{}, errors.New("Unauthorized")
		} else if authErr != nil {
			a.myLog(authErr)
			a.NullifyTokens(&w, r)
			return ClaimsType{}, errors.New("Unauthorized")
		}
		authTokenValue = AuthCookie.Value
	}

	token, _ := jwtGo.ParseWithClaims(authTokenValue, &ClaimsType{}, func(token *jwtGo.Token) (interface{}, error) {
		return ClaimsType{}, errors.New("Error processing token string claims")
	})
	tokenClaims, ok := token.Claims.(*ClaimsType)
	if !ok {
		return ClaimsType{}, errors.New("Error processing token string claims")
	}

	return *tokenClaims, nil
}

func (a *Auth) myLog(stoofs interface{}) {
	if a.options.Debug {
		log.Println(stoofs)
	}
}

func setHeader(w http.ResponseWriter, header string, value string) {
	w.Header().Set(header, value)
}
