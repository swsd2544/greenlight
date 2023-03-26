package main

import (
	"expvar"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func (app *application) routes() http.Handler {
	router := httprouter.New()

	router.NotFound = http.HandlerFunc(app.notFoundResponse)
	router.MethodNotAllowed = http.HandlerFunc(app.methodNotAllowedResponse)

	router.HandlerFunc(http.MethodGet, "/v1/healthcheck", app.healthcheckHandler)

	router.Handler(http.MethodGet, "/v1/movies", otelhttp.NewHandler(app.requirePermission("movies:read", app.listMoviesHandler), "listMovies"))
	router.Handler(http.MethodPost, "/v1/movies", otelhttp.NewHandler(app.requirePermission("movies:write", app.createMovieHandler), "createMovie"))
	router.Handler(http.MethodGet, "/v1/movies/:id", otelhttp.NewHandler(app.requirePermission("movies:read", app.showMovieHandler), "showMovie"))
	router.Handler(http.MethodPatch, "/v1/movies/:id", otelhttp.NewHandler(app.requirePermission("movies:write", app.updateMovieHandler), "updateMovie"))
	router.Handler(http.MethodDelete, "/v1/movies/:id", otelhttp.NewHandler(app.requirePermission("movies:write", app.deleteMovieHandler), "deleteMovie"))

	router.Handler(http.MethodPost, "/v1/users", otelhttp.NewHandler(http.HandlerFunc(app.registerUserHandler), "registerUser"))
	router.Handler(http.MethodPut, "/v1/users/activated", otelhttp.NewHandler(http.HandlerFunc(app.activateUserHandler), "activateUser"))

	router.Handler(http.MethodPost, "/v1/tokens/authentication", otelhttp.NewHandler(http.HandlerFunc(app.createAuthenticationHandler), "createAuthentication"))

	router.Handler(http.MethodGet, "/debug/vars", expvar.Handler())

	return app.metrics(app.recoverPanic(app.enableCORS(app.rateLimit(app.authenticate(router)))))
}
