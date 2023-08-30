package authentication

import (
	"bufio"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

type BasicAuthHandler struct {
	Credentials map[string]string
	Loaded      bool
}

func (bah *BasicAuthHandler) BasicAuth(path string) func(handler http.Handler) http.Handler {
	return func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, rq *http.Request) {
			if !bah.Loaded {
				bah.Credentials = make(map[string]string)
				if path != "" {
					file, err := os.Open(path)
					if err != nil {
						fmt.Printf("cannot open password file '%s': file does not exist\n", path)
						unauthorised(rw)
						return
					}
					defer file.Close()

					scanner := bufio.NewScanner(file)
					for scanner.Scan() {
						line := strings.TrimSpace(scanner.Text())
						if line != "" && !strings.HasPrefix(line, "#") {
							parts := strings.Split(line, ":")
							if len(parts) == 2 {
								bah.Credentials[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
							}
						}
					}
				}
				bah.Loaded = true
			}

			u, p, ok := rq.BasicAuth()
			if !ok || len(strings.TrimSpace(u)) < 1 || len(strings.TrimSpace(p)) < 1 {
				unauthorised(rw)
				return
			}

			passwd := bah.Credentials[u]
			if passwd == "" {
				unauthorised(rw)
				return
			}

			// This is a dummy check for credentials.
			if !comparePasswords(passwd, []byte(p)) {
				unauthorised(rw)
				return
			}

			// If required, Context could be updated to include authentication
			// related data so that it could be used in consequent steps.
			handler.ServeHTTP(rw, rq)
		})
	}
}

func unauthorised(rw http.ResponseWriter) {
	rw.Header().Set("WWW-Authenticate", "Basic realm=Restricted")
	rw.WriteHeader(http.StatusUnauthorized)
}

func HashAndSalt(pwd []byte) string {

	// Use GenerateFromPassword to hash & salt pwd.
	// MinCost is just an integer constant provided by the bcrypt
	// package along with DefaultCost & MaxCost.
	// The cost can be any value you want provided it isn't lower
	// than the MinCost (4)
	hash, err := bcrypt.GenerateFromPassword(pwd, bcrypt.MinCost)
	if err != nil {
		slog.Error("cannot generate hash from password", "error", err, "component", "authenticator")
	}
	// GenerateFromPassword returns a byte slice so we need to
	// convert the bytes to a string and return it
	return string(hash)
}

func comparePasswords(hashedPwd string, plainPwd []byte) bool {
	// Since we'll be getting the hashed password from the DB it
	// will be a string so we'll need to convert it to a byte slice
	byteHash := []byte(hashedPwd)
	err := bcrypt.CompareHashAndPassword(byteHash, plainPwd)
	if err != nil {
		slog.Error("cannot compare hash", "error", err, "component", "authenticator")
		return false
	}

	return true
}
