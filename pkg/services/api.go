package services

import (
	"context"
	"net/http"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/ogen-go/ogen/ogenerrors"
	"go.uber.org/zap"

	ht "github.com/ogen-go/ogen/http"
	"github.com/Abu103/teldrive/internal/api"
	"github.com/Abu103/teldrive/internal/cache"
	"github.com/Abu103/teldrive/internal/config"
	"github.com/Abu103/teldrive/internal/events"
	"github.com/Abu103/teldrive/internal/logging"
	"github.com/Abu103/teldrive/internal/tgc"
	"github.com/Abu103/teldrive/internal/utils"
	"github.com/Abu103/teldrive/internal/version"
	"github.com/Abu103/teldrive/pkg/models"
	"gorm.io/gorm"
)

type apiService struct {
	db          *gorm.DB
	cnf         *config.ServerCmdConfig
	cache       cache.Cacher
	tgdb        *gorm.DB
	worker      *tgc.BotWorker
	middlewares []telegram.Middleware
	events      *events.Recorder
}

func (a *apiService) VersionVersion(ctx context.Context) (*api.ApiVersion, error) {
	return version.GetVersionInfo(), nil
}

func (a *apiService) EventsGetEvents(ctx context.Context) ([]api.Event, error) {
	//Get latest events within 5 minutes
	res := []models.Event{}
	a.db.Model(&models.Event{}).Where("created_at > ?", time.Now().UTC().Add(-5*time.Minute).Format(time.RFC3339)).
		Order("created_at desc").Find(&res)
	return utils.Map(res, func(item models.Event) api.Event {
		return api.Event{
			ID:        item.ID,
			Type:      item.Type,
			CreatedAt: item.CreatedAt,
			Source: api.EventSource{
				ID:           item.Source.Data().ID,
				Type:         api.EventSourceType(item.Source.Data().Type),
				Name:         item.Source.Data().Name,
				ParentId:     item.Source.Data().ParentID,
				DestParentId: api.NewOptString(item.Source.Data().DestParentID),
			},
		}
	}), nil
}

func (a *apiService) NewError(ctx context.Context, err error) *api.ErrorStatusCode {
	var (
		code     = http.StatusInternalServerError
		message  = http.StatusText(code)
		ogenErr  ogenerrors.Error
		apiError *apiError
	)
	switch {
	case errors.Is(err, ht.ErrNotImplemented):
		code = http.StatusNotImplemented
		message = http.StatusText(code)
	case errors.As(err, &ogenErr):
		code = ogenErr.Code()
		message = ogenErr.Error()
	case errors.As(err, &apiError):
		if apiError.code == 0 {
			code = http.StatusInternalServerError
			message = http.StatusText(code)
		} else {
			code = apiError.code
			message = apiError.Error()
		}
		logging.FromContext(ctx).Error("api error", zap.Error(apiError))
	}
	return &api.ErrorStatusCode{StatusCode: code, Response: api.Error{Code: code, Message: message}}
}

func NewApiService(db *gorm.DB,
	cnf *config.ServerCmdConfig,
	cache cache.Cacher,
	tgdb *gorm.DB,
	worker *tgc.BotWorker,
	events *events.Recorder) *apiService {
	return &apiService{
		db:          db,
		cnf:         cnf,
		cache:       cache,
		tgdb:        tgdb,
		worker:      worker,
		middlewares: tgc.NewMiddleware(&cnf.TG, tgc.WithFloodWait(), tgc.WithRateLimit()),
		events:      events,
	}
}

type extendedService struct {
	api *apiService
}

func NewExtendedService(api *apiService) *extendedService {
	return &extendedService{api: api}
}

type extendedMiddleware struct {
	next *api.Server
	srv  *extendedService
}

func (m *extendedMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := m.next.FindRoute(r.Method, r.URL.Path)
	if !ok {
		m.next.ServeHTTP(w, r)
		return
	}
	switch route.Name() {
	case api.AuthWsOperation:
		m.srv.AuthWs(w, r)
		return
	case api.FilesStreamOperation:
		args := route.Args()
		m.srv.FilesStream(w, r, args[0], 0)
		return
	case api.SharesStreamOperation:
		args := route.Args()
		m.srv.SharesStream(w, r, args[0], args[1])
		return
	}
	m.next.ServeHTTP(w, r)
}

func NewExtendedMiddleware(next *api.Server, srv *extendedService) *extendedMiddleware {
	return &extendedMiddleware{next: next, srv: srv}
}

type apiError struct {
	err  error
	code int
}

func (a apiError) Error() string {
	return a.err.Error()
}

func (a *apiError) Unwrap() error {
	return a.err
}

var (
	_ api.Handler = (*apiService)(nil)
	_ error       = apiError{}
)
