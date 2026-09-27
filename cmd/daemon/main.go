package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alfred-identity/web/internal/appprocess"
	"github.com/alfred-identity/web/internal/config"
	"github.com/alfred-identity/web/internal/crypto"
	"github.com/alfred-identity/web/internal/db"
	"github.com/alfred-identity/web/internal/discord"
	"github.com/alfred-identity/web/internal/httpapi"
	"github.com/alfred-identity/web/internal/metrics"
	"github.com/alfred-identity/web/internal/presence"
	"github.com/alfred-identity/web/internal/sso"
	"github.com/alfred-identity/web/internal/store"
	"github.com/alfred-identity/web/internal/web"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.LUTC)

	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	logHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	processFlag := flag.String("process", strings.TrimSpace(os.Getenv("PROCESS")), "web, discord, or all (default all)")
	flag.Parse()
	role, err := appprocess.ParseRole(*processFlag)
	if err != nil {
		logger.Error("process", "err", err)
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}
	if role.WantsHTTP() {
		cfg.ApplyPlatformPort()
	}
	logger.Info("config loaded",
		"process", string(role),
		"http_addr", cfg.HTTPAddr,
		"ws_path", cfg.WSPath,
		"discord_enabled", cfg.DiscordEnabled,
		"protocol_version", cfg.ProtocolVersion,
		"eq_login_upstream", cfg.EQLoginUpstream,
		"log_level", level.String(),
	)

	if role.WantsDiscordGateway() && !cfg.DiscordEnabled {
		logger.Error("discord process requires DISCORD_ENABLED=true")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	sqlDB, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer sqlDB.Close()
	logger.Info("database connected")

	if err := db.Migrate(sqlDB); err != nil {
		logger.Error("migrate", "err", err)
		os.Exit(1)
	}
	logger.Info("migrations ok")

	aead, err := crypto.NewAEAD(cfg.DataEncryptionKey)
	if err != nil {
		logger.Error("crypto", "err", err)
		os.Exit(1)
	}

	st := &store.Store{DB: sqlDB, AEAD: aead, Key: cfg.DataEncryptionKey}
	if _, err := st.EnsureDefaultGroup(ctx); err != nil {
		logger.Error("default group", "err", err)
		os.Exit(1)
	}

	var bot *discord.Bot
	if cfg.DiscordEnabled && (role.WantsDiscordGateway() || role.WantsShareREST()) {
		bot, err = discord.New(cfg, st, logger, role.WantsDiscordGateway())
		if err != nil {
			logger.Error("discord", "err", err)
			os.Exit(1)
		}
		if role.WantsDiscordGateway() {
			if err := bot.Open(); err != nil {
				logger.Error("discord open", "err", err)
				os.Exit(1)
			}
			logger.Info("discord gateway enabled", "guild_id", cfg.DiscordGuildID)
		} else {
			logger.Info("discord rest client enabled (share DMs; no gateway)")
		}
		defer bot.Close()
	} else if !cfg.DiscordEnabled {
		logger.Info("discord disabled (mock/CI mode)")
	}

	if role.WantsHTTP() {
		runHTTP(ctx, cancel, cfg, sqlDB, st, bot, logger)
	} else {
		<-ctx.Done()
	}

	logger.Info("shutting down")
}

func runHTTP(ctx context.Context, cancel context.CancelFunc, cfg config.Config, sqlDB *sql.DB, st *store.Store, bot *discord.Bot, logger *slog.Logger) {
	pres := presence.New(cfg.PresenceTTL)
	hub := &sso.Hub{
		Store:             st,
		Presence:          pres,
		ProtocolVersion:   cfg.ProtocolVersion,
		AdminRoleID:       cfg.DiscordAdminRoleID,
		BootstrapAdminIDs: cfg.DiscordBootstrapAdmins,
		LoginUpstream:     cfg.EQLoginUpstream,
		Log:               logger,
	}
	hub.SetRatePerMin(cfg.LoginAuthRatePerMin)
	if bot != nil {
		hub.ShareNotifier = bot
	}

	metricsSampler := &metrics.Sampler{
		Store: st,
		Log:   logger,
		Sources: metrics.Sources{
			GUIConnections: func() int { return len(hub.Connections()) },
			GameSessions:   pres.Count,
			DBPingLatency:  metrics.PingLatencyMS(sqlDB),
			DBPoolStats:    metrics.PoolStats(sqlDB),
		},
	}
	go metricsSampler.Run(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", httpapi.Health(func() bool {
		return sqlDB.PingContext(context.Background()) == nil
	}))
	mux.HandleFunc("/open-alfred", web.HandleOpenAlfred)
	mux.Handle(cfg.WSPath, hub)

	if cfg.WebEnabled {
		sessionKey := cfg.WebSessionKey
		if len(sessionKey) == 0 {
			derived, derr := crypto.DeriveKey(cfg.DataEncryptionKey, "web-session")
			if derr != nil {
				logger.Error("derive web session key", "err", derr)
				os.Exit(1)
			}
			sessionKey = derived
		}
		webSrv := web.New(web.Options{
			Store:             st,
			Hub:               hub,
			Presence:          pres,
			Log:               logger,
			SessionKey:        sessionKey,
			PublicURL:         cfg.WebPublicURL,
			ClientID:          cfg.DiscordClientID,
			ClientSecret:      cfg.DiscordClientSecret,
			GuildID:           cfg.DiscordGuildID,
			AccessRoleID:      cfg.WebAccessRoleID,
			BootstrapAdminIDs: cfg.DiscordBootstrapAdmins,
			AdminRoleID:       cfg.DiscordAdminRoleID,
			SSOSourceName:     cfg.WebSSOSourceName,
			RequireAccountACL: cfg.RequireAccountACL,
		})
		webSrv.Mount(mux)
		logger.Info("web admin enabled",
			"url", cfg.WebPublicURL+web.BasePath+"/",
			"access_role", cfg.WebAccessRoleID,
		)
	}

	handler := httpapi.RequestLog(logger, mux)
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		logger.Info("listening", "addr", cfg.HTTPAddr, "ws", cfg.WSPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()
	shutdownCtx, c2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer c2()
	_ = srv.Shutdown(shutdownCtx)
}
