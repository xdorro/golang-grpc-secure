package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/wire"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/xdorro/golang-grpc-base-project/internal/repo"
	"github.com/xdorro/golang-grpc-base-project/internal/service"
)

// ProviderServerSet is server providers.
var ProviderServerSet = wire.NewSet(NewServer)
var _ IServer = (*Server)(nil)

// IServer is the interface for the server
type IServer interface {
	Close() error

	httpGrpcRouter() http.Handler
	newGRPCServer(tlsCredentials credentials.TransportCredentials, service service.IService)
	newHTTPServer(tlsCredentials credentials.TransportCredentials, appPort string)
}

// Server is server struct.
type Server struct {
	ctx        context.Context
	log        *zap.Logger
	repo       repo.IRepo
	grpcServer *grpc.Server
	httpServer *runtime.ServeMux
	server     *http.Server
}

// NewServer creates a new server.
func NewServer(
	ctx context.Context, log *zap.Logger, repo repo.IRepo, service service.IService,
) IServer {
	s := &Server{
		ctx:  ctx,
		log:  log,
		repo: repo,
	}

	cert := viper.GetString("APP_CERT")
	key := viper.GetString("APP_KEY")

	tlsCredentials, err := loadTLSCredentials(cert, key)
	if err != nil {
		log.Panic("cannot load TLS credentials: ", zap.Error(err))
	}

	appPort := fmt.Sprintf(":%d", viper.GetInt("APP_PORT"))
	log.Info(fmt.Sprintf("Serving on https://localhost%s", appPort))

	s.newHTTPServer(tlsCredentials, appPort)
	s.newGRPCServer(tlsCredentials, service)
	s.server = &http.Server{
		Addr:    appPort,
		Handler: s.httpGrpcRouter(),
	}

	go func() {
		if err = s.server.ListenAndServeTLS(cert, key); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Panic("http.ListenAndServeTLS()", zap.Error(err))
		}
	}()

	return s
}

// Close closes the server.
func (s *Server) Close() error {
	s.log.Info("Closing server...")
	s.grpcServer.GracefulStop()

	if err := s.server.Shutdown(s.ctx); err != nil {
		return err
	}

	if err := s.repo.Close(); err != nil {
		return err
	}

	return nil
}

// httpGrpcRouter is http grpc router.
func (s *Server) httpGrpcRouter() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.Contains(r.Header.Get("Content-Type"), "application/grpc") {
			s.grpcServer.ServeHTTP(w, r)
			return
		}

		// middleware that adds CORS headers to the response.
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "http://localhost:3000")
		h.Set("Access-Control-Allow-Credentials", "true")

		if r.Method == http.MethodOptions {
			h.Set("Access-Control-Methods", "POST, PUT, PATCH, DELETE")
			h.Set("Access-Control-Allow-Headers", "Access-Control-Allow-Origin,Content-Type")
			h.Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		s.httpServer.ServeHTTP(w, r)
	})
}

// loadTLSCredentials loads TLS credentials from the configuration
func loadTLSCredentials(cert, key string) (credentials.TransportCredentials, error) {
	// Load server's certificate and private key
	serverCert, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}

	// Create the credentials and return it
	config := &tls.Config{
		Certificates:       []tls.Certificate{serverCert},
		ClientAuth:         tls.RequireAndVerifyClientCert,
		InsecureSkipVerify: true,
	}

	return credentials.NewTLS(config), nil
}
