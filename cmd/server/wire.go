package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	emailadapter "github.com/johnnycube/cairn-core/internal/adapter/secondary/email"
	geocodeadapter "github.com/johnnycube/cairn-core/internal/adapter/secondary/geocode"
	natsadapter "github.com/johnnycube/cairn-core/internal/adapter/secondary/nats"
	"github.com/johnnycube/cairn-core/internal/adapter/secondary/postgres"
	s3adapter "github.com/johnnycube/cairn-core/internal/adapter/secondary/s3"
	"github.com/johnnycube/cairn-core/internal/auth"
	"github.com/johnnycube/cairn-core/internal/config"
	"github.com/johnnycube/cairn-core/internal/domain"
	dmatch "github.com/johnnycube/cairn-core/internal/domain/match"
	"github.com/johnnycube/cairn-core/internal/port"
	"github.com/johnnycube/cairn-core/internal/usecase/activity"
	"github.com/johnnycube/cairn-core/internal/usecase/admin"
	authuc "github.com/johnnycube/cairn-core/internal/usecase/auth"
	"github.com/johnnycube/cairn-core/internal/usecase/besteffort"
	"github.com/johnnycube/cairn-core/internal/usecase/enrollment"
	geocodeuc "github.com/johnnycube/cairn-core/internal/usecase/geocode"
	"github.com/johnnycube/cairn-core/internal/usecase/invite"
	matchuc "github.com/johnnycube/cairn-core/internal/usecase/match"
	"github.com/johnnycube/cairn-core/internal/usecase/notification"
	"github.com/johnnycube/cairn-core/internal/usecase/segment"
	"github.com/johnnycube/cairn-core/internal/usecase/segmentrank"
	syncuc "github.com/johnnycube/cairn-core/internal/usecase/sync"
	"github.com/johnnycube/cairn-core/internal/usecase/trainingload"
)

// App is the constructed dependency graph the HTTP server uses.
//
// Building it lives in newApp; HTTP handlers (Connect-RPC, REST, GraphQL)
// reach into App's fields to invoke use cases. Keeping construction in
// one place makes the binary's surface obvious and testable — every
// production wire-up goes through here.
type App struct {
	// Adapters
	QuotaMaxActivities     int
	// DefaultPollInterval is the instance-wide reconcile cadence
	// (SchedulerConfig.PollingInterval) — surfaced to the UI so the
	// per-connection override setting can say what "instance default" means.
	DefaultPollInterval time.Duration
	Tx                     *postgres.TxManager
	Activities             *postgres.ActivityRepo
	UserSettings           *postgres.UserSettingsRepo
	Users                  *postgres.UserRepo
	Streams                *postgres.StreamRepo
	BestEfforts            *postgres.BestEffortRepo
	Segments               *postgres.SegmentRepo
	Metrics                *postgres.MetricRepo
	Notifications          *postgres.NotificationRepo
	NotificationPrefs      *postgres.NotificationPreferenceRepo
	WebhookEndpoints       *postgres.WebhookEndpointRepo
	QuietHours             *postgres.QuietHoursRepo
	NotificationDeliveries *postgres.NotificationDeliveryRepo
	Follows                *postgres.FollowRepo
	FederationKeys         *postgres.FederationKeyRepo
	FederationActors       *postgres.FederationActorRepo
	FederationFollows      *postgres.FederationFollowRepo
	FederationDeliveries   *postgres.FederationDeliveryRepo
	FederationBlocks       *postgres.FederationBlockRepo
	InboxDedup             *postgres.InboxDedupRepo
	FederationFeed         *postgres.FederationFeedRepo
	Visibility             *postgres.VisibilityRepo
	ShareLinks             *postgres.ShareLinkRepo
	Engagement             *postgres.EngagementRepo
	Blocks                 *postgres.BlockRepo
	Reports                *postgres.ReportRepo
	Moderation             *postgres.ModerationRepo
	Quotas                 *postgres.QuotaRepo
	Clubs                  *postgres.ClubRepo
	ExternalAccounts       *postgres.ExternalAccountRepo
	WorkerEnrollments      *postgres.WorkerEnrollmentRepo
	SigningKeys            *postgres.SigningKeyRepo
	OAuthTokens            *postgres.OAuthTokenRepo
	DeadLetters            *postgres.DeadLetterRepo
	ImportQueue            port.ImportQueueRepo
	UserProviderConfigs    *postgres.UserProviderConfigRepo
	ImportEvents           *postgres.ImportEventRepo
	AthleteProfiles        *postgres.AthleteProfileRepo
	Passkeys               *postgres.PasskeyRepo
	Sessions               *postgres.SessionRepo
	LinkedIdentities       *postgres.LinkedIdentityRepo

	// OIDCProviders are configured entirely from CAIRN_OIDC_* env vars; there
	// is no client database. Empty means OIDC sign-in is disabled.
	OIDCProviders []domain.OIDCProvider

	// Auth primitives
	PasswordHasher *auth.PasswordHasher
	SecretBox      *auth.SecretBox

	// NATS adapters. All four nil-by-default; populated only when
	// cfg.NATS.URL is set. Wire-order: Bus → SecretBox → CredentialIssuer
	// → AuthCallout subscriber. The RateLimiter sits on top of the
	// Bus's KV handle.
	NATSBus              *natsadapter.Bus
	NATSCredentialIssuer *natsadapter.CredentialIssuer
	NATSAuthCallout      *natsadapter.AuthCalloutSubscriber
	RateLimiter          port.RateLimiter

	// BlobStore is the S3-compatible blob adapter. Nil-by-default; populated
	// only when cfg.Storage.Configured() — i.e. CAIRN_STORAGE_ACCESS_KEY_ID
	// and CAIRN_STORAGE_SECRET_ACCESS_KEY are both set. Code paths that
	// emit presigned URLs (worker blob upload, user-facing raw-download
	// endpoint) check for nil and return Unavailable when the operator
	// hasn't configured storage.
	BlobStore port.BlobStore

	// Email is the outbound email adapter (port.EmailSender). Nil when
	// EMAIL_ADAPTER=none; callers skip the email channel.
	Email port.EmailSender

	// Use cases
	RecomputeActivity        *activity.RecomputeActivityFromSources
	IngestActivity           *activity.IngestActivityFromWorker
	ReclusterBucket          *matchuc.ReclusterBucket
	FieldOverrides           port.FieldOverrideRepo
	ClassOverrides           port.ClassificationOverrideRepo
	Attachments              port.AttachmentRepo
	SourceDenylist           port.SourceDenylistRepo
	Health                   port.HealthRepo
	ComputeBestEfforts       *besteffort.ComputeBestEffortsForSource
	MatchSegments            *segment.MatchSegmentsForActivity
	ComputeSegmentRanks      *segmentrank.ComputeSegmentRanks
	ComputeTrainingLoad      *trainingload.ComputeTrainingLoadForUser
	DispatchPR               *notification.DispatchPRNotifications
	DeliverNotifications     *notification.DeliverNotifications
	NotifyAccountNeedsReauth *notification.NotifyAccountNeedsReauth
	NotifyWorkerOffline      *notification.NotifyWorkerOffline
	ComputeStartPlace        *geocodeuc.ComputeStartPlace
	CreateUser               *admin.CreateUser
	BootstrapAdmin           *admin.BootstrapAdmin
	Invites                  *postgres.InviteRepo
	CreateInvite             *invite.CreateInvite
	RedeemInvite             *invite.RedeemInvite
	PATs                     *postgres.PATRepo
	AuthTokens               *postgres.AuthTokenRepo
	OAuth                    *postgres.OAuthServerRepo

	// ReconcileAccount is nil until a JobBus adapter (NATS) is wired.
	// The reconcile scheduler goroutine in serve.go checks for nil
	// before starting; admin endpoints likewise.
	ReconcileAccount *syncuc.ReconcileExternalAccount

	// Worker-enrollment use cases. CreateWorkerEnrollment + Revoke
	// work without any NATS adapter — the admin can pre-authorise
	// workers and generate tokens even before the auth-callout
	// handler exists. ProcessAuthCallout is built but not yet
	// reachable (needs NATS adapter to subscribe to $SYS.REQ.USER.AUTH).
	CreateWorkerEnrollment *enrollment.CreateWorkerEnrollment
	RevokeWorkerEnrollment *enrollment.RevokeWorkerEnrollment

	// ProcessAuthCallout exists only when the NATS adapter is wired —
	// it depends on the CredentialIssuer for JWT minting.
	ProcessAuthCallout *enrollment.ProcessAuthCallout

	// OIDCLogin is the user-facing OIDC use case. Wired only when at
	// least one oidc_client row is enabled — otherwise the routes
	// stay unregistered.
	OIDCLogin *authuc.OIDCLogin

	// Federation publishing context (Phase 3). PublicBaseURL is the
	// externally-visible origin used to build actor + object URLs;
	// FederationEnabled mirrors the instance flag so the post-ingest
	// publish step can short-circuit when federation is off.
	PublicBaseURL     string
	FederationEnabled bool
}

func newApp(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, logger *slog.Logger) (*App, error) {
	tx := postgres.NewTxManager(pool)
	activities := postgres.NewActivityRepo(pool)
	settings := postgres.NewUserSettingsRepo(pool)
	users := postgres.NewUserRepo(pool)
	streams := postgres.NewStreamRepo(pool)
	bestEffortRepo := postgres.NewBestEffortRepo(pool)
	segmentRepo := postgres.NewSegmentRepo(pool)
	metricRepo := postgres.NewMetricRepo(pool)
	notificationRepo := postgres.NewNotificationRepo(pool)
	followRepo := postgres.NewFollowRepo(pool)
	visibilityRepo := postgres.NewVisibilityRepo(pool)
	shareLinkRepo := postgres.NewShareLinkRepo(pool)
	engagementRepo := postgres.NewEngagementRepo(pool)
	blockRepo := postgres.NewBlockRepo(pool)
	reportRepo := postgres.NewReportRepo(pool)
	moderationRepo := postgres.NewModerationRepo(pool)
	quotaRepo := postgres.NewQuotaRepo(pool)
	clubRepo := postgres.NewClubRepo(pool)
	externalAccountRepo := postgres.NewExternalAccountRepo(
		pool,
		cfg.Scheduler.PollingInterval,
		cfg.Scheduler.WebhookDriftInterval,
	)
	workerEnrollmentRepo := postgres.NewWorkerEnrollmentRepo(pool)
	signingKeyRepo := postgres.NewSigningKeyRepo(pool)
	sessionRepo := postgres.NewSessionRepo(pool)
	linkedIdentityRepo := postgres.NewLinkedIdentityRepo(pool)

	// OIDC providers come from CAIRN_OIDC_* env vars (no client DB). Each
	// warning names a provider entry that was skipped due to missing config.
	oidcProviders, oidcWarnings := config.LoadOIDCProviders(os.Getenv)
	for _, w := range oidcWarnings {
		logger.Warn("oidc provider config", "detail", w)
	}

	hasher := auth.NewPasswordHasher(auth.PasswordParams{
		Time:    cfg.Auth.Argon2Time,
		Memory:  cfg.Auth.Argon2Memory,
		Threads: cfg.Auth.Argon2Threads,
		KeyLen:  cfg.Auth.Argon2KeyLen,
	})

	secretBox, err := auth.NewSecretBoxFromMasterKey(cfg.Auth.MasterEncryptionKey)
	if err != nil {
		return nil, err
	}

	oauthTokenRepo := postgres.NewOAuthTokenRepo(pool, secretBox)
	federationKeyRepo := postgres.NewFederationKeyRepo(pool, secretBox)
	federationActorRepo := postgres.NewFederationActorRepo(pool)
	federationFollowRepo := postgres.NewFederationFollowRepo(pool)
	federationDeliveryRepo := postgres.NewFederationDeliveryRepo(pool)
	federationBlockRepo := postgres.NewFederationBlockRepo(pool)
	inboxDedupRepo := postgres.NewInboxDedupRepo(pool)
	federationFeedRepo := postgres.NewFederationFeedRepo(pool)
	deadLetterRepo := postgres.NewDeadLetterRepo(pool)
	importQueueRepo := postgres.NewImportQueueRepo(pool)
	userProviderConfigRepo := postgres.NewUserProviderConfigRepo(pool, secretBox)
	importEventRepo := postgres.NewImportEventRepo(pool)
	athleteProfileRepo := postgres.NewAthleteProfileRepo(pool)
	passkeyRepo := postgres.NewPasskeyRepo(pool)
	fieldOverrides := postgres.NewFieldOverrideRepo(pool)
	classOverrides := postgres.NewClassificationOverrideRepo(pool)
	attachments := postgres.NewAttachmentRepo(pool)
	sourceDenylist := postgres.NewSourceDenylistRepo(pool)
	healthRepo := postgres.NewHealthRepo(pool)

	recompute := activity.NewRecomputeActivityFromSources(
		activities,
		settings,
		athleteProfileRepo,
		fieldOverrides,
		classOverrides,
		tx,
		nil, // default time.Now
	)
	ingest := activity.NewIngestActivityFromWorker(
		activities,
		streams,
		settings,
		recompute,
		tx,
		nil, // default time.Now
		nil, // default uuid.NewV7
	)
	reclusterBucket := matchuc.NewReclusterBucket(
		activities,
		recompute,
		sourceDenylist,
		tx,
		nil, // default uuid.NewV7
		dmatch.DefaultOptions(),
		logger,
	)
	computeBestEfforts := besteffort.NewComputeBestEffortsForSource(
		activities,
		streams,
		bestEffortRepo,
		tx,
		nil, // default time.Now
		nil, // default uuid.NewV7
	)
	computeSegmentRanks := segmentrank.NewComputeSegmentRanks(segmentRepo, tx)
	matchSegments := segment.NewMatchSegmentsForActivity(
		activities,
		streams,
		segmentRepo,
		tx,
		computeSegmentRanks,
		nil,                      // default time.Now
		nil,                      // default uuid.NewV7
		domain.MatchTolerances{}, // zero → DefaultMatchTolerances
		nil,                      // default slog.Default
	)
	computeTrainingLoad := trainingload.NewComputeTrainingLoadForUser(
		activities,
		metricRepo,
		tx,
		nil, // default time.Now
		nil, // default uuid.NewV7
	)
	// Email sender (gated on EMAIL_ADAPTER) + notification side-channel delivery.
	// Built here so DispatchPR can fan PR notifications out to email.
	var emailSender port.EmailSender
	switch cfg.Email.Adapter {
	case "smtp":
		es, err := emailadapter.NewSMTP(cfg.Email)
		if err != nil {
			return nil, err
		}
		emailSender = es
		logger.Info("email wired", "adapter", "smtp", "host", cfg.Email.SMTPHost, "from", cfg.Email.FromAddress)
	case "", "none":
		logger.Info("email disabled: EMAIL_ADAPTER=none")
	default:
		logger.Warn("email adapter not implemented; disabling", "adapter", cfg.Email.Adapter)
	}
	notificationPrefRepo := postgres.NewNotificationPreferenceRepo(pool)
	webhookEndpointRepo := postgres.NewWebhookEndpointRepo(pool, secretBox)
	quietHoursRepo := postgres.NewQuietHoursRepo(pool)
	notificationDeliveryRepo := postgres.NewNotificationDeliveryRepo(pool)
	deliverNotifs := notification.NewDeliverNotifications(
		users, notificationRepo, notificationPrefRepo, emailSender, webhookEndpointRepo, quietHoursRepo, notificationDeliveryRepo, cfg.HTTP.PublicBaseURL, logger,
	)

	dispatchPR := notification.NewDispatchPRNotifications(
		segmentRepo,
		notificationRepo,
		tx,
		deliverNotifs,
		nil, // default time.Now
		nil, // default uuid.NewV7
	)
	notifyReauth := notification.NewNotifyAccountNeedsReauth(
		externalAccountRepo,
		notificationRepo,
		tx,
		deliverNotifs,
		nil, // default time.Now
		nil, // default uuid.NewV7
	)
	notifyWorkerOffline := notification.NewNotifyWorkerOffline(
		users,
		notificationRepo,
		tx,
		deliverNotifs,
		nil, // default time.Now
		nil, // default uuid.NewV7
	)
	createUser := admin.NewCreateUser(users, hasher, nil, nil)
	bootstrap := admin.NewBootstrapAdmin(users, createUser)
	inviteRepo := postgres.NewInviteRepo(pool)
	createInvite := invite.NewCreateInvite(inviteRepo, nil)
	redeemInvite := invite.NewRedeemInvite(inviteRepo, createUser, nil)
	patRepo := postgres.NewPATRepo(pool)
	authTokenRepo := postgres.NewAuthTokenRepo(pool)
	oauthServerRepo := postgres.NewOAuthServerRepo(pool)

	// Worker-enrollment use cases. CreateWorkerEnrollment is the
	// admin-side flow ("generate a token I can put in a worker's env").
	// RevokeWorkerEnrollment is the kill-switch. ProcessAuthCallout
	// (built but not constructed here) needs a NATSCredentialIssuer
	// adapter from the NATS layer; we'll wire it once that exists.
	createEnrollment := enrollment.NewCreateWorkerEnrollment(
		workerEnrollmentRepo,
		nil, // default time.Now
		nil, // default uuid.NewV7
		nil, // default crypto/rand
	)
	revokeEnrollment := enrollment.NewRevokeWorkerEnrollment(
		workerEnrollmentRepo,
		nil, // default time.Now
	)

	// ------------------------------------------------------------------
	// NATS adapter wiring (gated on cfg.NATS.URL)
	//
	// When CAIRN_NATS_URL is unset, the bus stays nil and every
	// dependent capability (reconcile scheduler, auth-callout subscriber,
	// rate limiter, request/reply token-fetch) silently skips. This
	// keeps single-binary dev setups runnable without NATS.
	//
	// When set, we:
	//   1. Connect (eager).
	//   2. Bootstrap canonical streams + KV/OS buckets (idempotent).
	//   3. Construct CredentialIssuer + AuthCalloutSubscriber.
	//   4. Build the rate limiter on the cairn_rate_limits KV bucket.
	//   5. Build ProcessAuthCallout (use case) on top of the issuer.
	//   6. Build ReconcileExternalAccount on top of the bus.
	// ------------------------------------------------------------------
	var (
		bus              *natsadapter.Bus
		credentialIssuer *natsadapter.CredentialIssuer
		authCallout      *natsadapter.AuthCalloutSubscriber
		rateLimiter      port.RateLimiter
		processAuth      *enrollment.ProcessAuthCallout
		reconcile        *syncuc.ReconcileExternalAccount
	)

	if cfg.NATS.URL != "" {
		bus, err = natsadapter.NewBus(ctx, cfg.NATS, logger)
		if err != nil {
			return nil, err
		}

		// Stream / KV / Object-Store bootstrap. Idempotent — see
		// docs/architecture.md §2 for the configs.
		if err := bus.BootstrapStreams(ctx); err != nil {
			return nil, err
		}

		credentialIssuer = natsadapter.NewCredentialIssuer(signingKeyRepo, secretBox)

		// Build the rate limiter on the rate-limits KV bucket. The
		// server-side limiter is intentionally permissive (nil
		// capacities = no buckets configured = always allow). Workers
		// pass their own per-provider capacities at construction —
		// rate-limit policy is a worker concern, not a core concern.
		rlKV, kvErr := bus.KV(natsadapter.KVRateLimits)
		if kvErr != nil {
			return nil, kvErr
		}
		rateLimiter = natsadapter.NewRateLimiter(rlKV, nil)

		// Worker auth-callout use case. Needs the issuer.
		processAuth = enrollment.NewProcessAuthCallout(
			workerEnrollmentRepo,
			credentialIssuer,
			tx,
			nil,                 // default time.Now
			nil,                 // default uuid.NewV7
			cfg.NATS.JobAckWait, // reuse as JWT TTL — typically 5min-24h
		)

		authCallout = natsadapter.NewAuthCalloutSubscriber(
			bus, processAuth, credentialIssuer, logger,
		)
		// Start() is invoked from serve.go so the subscription's
		// lifecycle matches the HTTP server's.

		// Reconciliation scheduler use case now has a real bus. The budget
		// gate skips accounts whose provider API budget is spent (live KV
		// state the workers sync from real usage headers) — polling an
		// exhausted provider only burns budget the backfill needs.
		reconcile = syncuc.NewReconcileExternalAccount(
			externalAccountRepo,
			bus,
			func(ctx context.Context, provider string) bool {
				_, exhausted := providerBudget(ctx, bus, provider)
				return exhausted
			},
			nil, // default time.Now
			nil, // default uuid.NewV7
		)
	} else {
		logger.Info("nats: CAIRN_NATS_URL unset — async layer disabled, single-process mode")
	}

	// OIDC login use case. Constructed only when at least one provider is
	// configured; the HTTP layer gates route registration on OIDCLogin being
	// non-nil.
	var oidcLogin *authuc.OIDCLogin
	if len(oidcProviders) > 0 {
		oidcLogin = authuc.NewOIDCLogin(
			oidcProviders,
			linkedIdentityRepo,
			sessionRepo,
			users,
			cfg.Auth.SessionTTL,
			cfg.HTTP.PublicBaseURL,
		)
	}

	// S3-compatible blob store. Gated on storage credentials being set —
	// instances that don't yet have object storage configured come up
	// in a "no raw blobs" mode; ingest pipelines still run end-to-end.
	var blobStore port.BlobStore
	if cfg.Storage.Configured() {
		bs, err := s3adapter.New(cfg.Storage)
		if err != nil {
			return nil, err
		}
		// Create the configured bucket (CAIRN_STORAGE_BUCKET) if it's missing —
		// part of core bootstrap so a fresh deployment needs no out-of-band step.
		if err := bs.EnsureBucket(ctx); err != nil {
			return nil, fmt.Errorf("ensure storage bucket: %w", err)
		}
		if err := bs.EnsureLifecycleRule(ctx, "cairn-transfer-expiry", transferPrefix, int32(cfg.Storage.TransferExpiryDays)); err != nil {
			return nil, fmt.Errorf("ensure transfer lifecycle: %w", err)
		}
		blobStore = bs
		logger.Info("blob store wired",
			"endpoint", cfg.Storage.Endpoint,
			"bucket", cfg.Storage.Bucket,
			"path_style", cfg.Storage.UsePathStyle)
	} else {
		logger.Info("blob store not configured: CAIRN_STORAGE_ACCESS_KEY_ID/SECRET_ACCESS_KEY unset")
	}

	// Reverse-geocoder for activity start-location subtitles. Gated on
	// CAIRN_GEOCODER_ENABLED (default true). The adapter enforces the
	// provider's usage policy internally; the backfiller in serve.go drives it.
	var computeStartPlace *geocodeuc.ComputeStartPlace
	if cfg.Geocoder.Enabled {
		geocoder := geocodeadapter.New(cfg.Geocoder)
		computeStartPlace = geocodeuc.NewComputeStartPlace(activities, streams, geocoder, logger)
		logger.Info("geocoder wired", "url", cfg.Geocoder.URL, "min_interval", cfg.Geocoder.MinInterval)
	} else {
		logger.Info("geocoder disabled: CAIRN_GEOCODER_ENABLED=false")
	}

	return &App{
		Tx:                       tx,
		Activities:               activities,
		UserSettings:             settings,
		Users:                    users,
		Streams:                  streams,
		BestEfforts:              bestEffortRepo,
		Segments:                 segmentRepo,
		Metrics:                  metricRepo,
		Notifications:            notificationRepo,
		NotificationPrefs:        notificationPrefRepo,
		WebhookEndpoints:         webhookEndpointRepo,
		QuietHours:               quietHoursRepo,
		NotificationDeliveries:   notificationDeliveryRepo,
		Follows:                  followRepo,
		FederationKeys:           federationKeyRepo,
		FederationActors:         federationActorRepo,
		FederationFollows:        federationFollowRepo,
		FederationDeliveries:     federationDeliveryRepo,
		FederationBlocks:         federationBlockRepo,
		InboxDedup:               inboxDedupRepo,
		FederationFeed:           federationFeedRepo,
		Visibility:               visibilityRepo,
		ShareLinks:               shareLinkRepo,
		Engagement:               engagementRepo,
		Blocks:                   blockRepo,
		Reports:                  reportRepo,
		Moderation:               moderationRepo,
		Quotas:                   quotaRepo,
		QuotaMaxActivities:       cfg.Quota.MaxActivitiesPerUser,
		DefaultPollInterval:      cfg.Scheduler.PollingInterval,
		Clubs:                    clubRepo,
		ExternalAccounts:         externalAccountRepo,
		WorkerEnrollments:        workerEnrollmentRepo,
		SigningKeys:              signingKeyRepo,
		OAuthTokens:              oauthTokenRepo,
		DeadLetters:              deadLetterRepo,
		ImportQueue:              importQueueRepo,
		UserProviderConfigs:      userProviderConfigRepo,
		AthleteProfiles:          athleteProfileRepo,
		Passkeys:                 passkeyRepo,
		ImportEvents:             importEventRepo,
		PasswordHasher:           hasher,
		SecretBox:                secretBox,
		NATSBus:                  bus,
		NATSCredentialIssuer:     credentialIssuer,
		NATSAuthCallout:          authCallout,
		RateLimiter:              rateLimiter,
		BlobStore:                blobStore,
		Email:                    emailSender,
		Sessions:                 sessionRepo,
		OIDCProviders:            oidcProviders,
		LinkedIdentities:         linkedIdentityRepo,
		OIDCLogin:                oidcLogin,
		RecomputeActivity:        recompute,
		IngestActivity:           ingest,
		ReclusterBucket:          reclusterBucket,
		FieldOverrides:           fieldOverrides,
		ClassOverrides:           classOverrides,
		Attachments:              attachments,
		SourceDenylist:           sourceDenylist,
		Health:                   healthRepo,
		ComputeBestEfforts:       computeBestEfforts,
		MatchSegments:            matchSegments,
		ComputeSegmentRanks:      computeSegmentRanks,
		ComputeTrainingLoad:      computeTrainingLoad,
		DispatchPR:               dispatchPR,
		DeliverNotifications:     deliverNotifs,
		NotifyAccountNeedsReauth: notifyReauth,
		NotifyWorkerOffline:      notifyWorkerOffline,
		ComputeStartPlace:        computeStartPlace,
		ReconcileAccount:         reconcile,
		CreateWorkerEnrollment:   createEnrollment,
		RevokeWorkerEnrollment:   revokeEnrollment,
		ProcessAuthCallout:       processAuth,
		CreateUser:               createUser,
		BootstrapAdmin:           bootstrap,
		Invites:                  inviteRepo,
		CreateInvite:             createInvite,
		RedeemInvite:             redeemInvite,
		PATs:                     patRepo,
		AuthTokens:               authTokenRepo,
		OAuth:                    oauthServerRepo,
		PublicBaseURL:            cfg.HTTP.PublicBaseURL,
		FederationEnabled:        cfg.Federation.Enabled,
	}, nil
}
