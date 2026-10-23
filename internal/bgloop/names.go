package bgloop

// Loop names. Every loop the platform runs is named here, which is what
// bounds the loop label on the background series: a name is added beside the
// loop that uses it, never built from data. Two additions are derived from
// names here and stay bounded with them: a retention sweep reports under
// Retention + "_" + its sweep name (internal/platform/retention declares the
// sweeps), and a LISTEN connection under Listen* below.
const (
	// Request-path state the platform keeps in memory, swept on a timer.
	NameSessionGateCleanup       = "session_gate_cleanup"
	NameSessionEnrichmentCleanup = "session_enrichment_cleanup"
	NameSessionErrorsCleanup     = "session_errors_cleanup"
	NameSearchGateCleanup        = "search_gate_cleanup"
	NamePKCECleanup              = "pkce_cleanup"
	NameSessionCleanup           = "session_cleanup"
	NameOAuthCleanup             = "oauth_cleanup"
	NameOAuthStoreCleanup        = "oauth_store_cleanup"

	// Retention and maintenance sweeps.
	NameAuditMaintenance  = "audit_maintenance"
	NameAuthEventsPrune   = "authevents_prune"
	NameCallCatalogSweep  = "call_catalog_sweep"
	NameIndexJobRetention = "indexjob_retention"
	NameRetention         = "retention"

	// Capacity: the table sampler, the shared bucket-usage flush and the
	// full bucket listing (internal/platform/capacity).
	NameCapacityTables    = "capacity_tables"
	NameStorageUsageFlush = "storage_usage_flush"
	NameStorageScan       = "storage_scan"
	// NameStorageHistory samples each store's usage and capacity, keeps the
	// history and evaluates the storage alerts (internal/platform/storagecap).
	NameStorageHistory = "storage_history"

	// Queue workers and their sub-loops.
	NameIndexJobWorker      = "indexjob_worker"
	NameIndexJobHeartbeat   = "indexjob_heartbeat"
	NameIndexJobReaper      = "indexjob_reaper"
	NameIndexJobReconciler  = "indexjob_reconciler"
	NameIndexJob            = "indexjob"
	NameNotifyWorker        = "notification_worker"
	NameNotifyDelivery      = "notification_delivery"
	NameScriptScheduler     = "script_scheduler"
	NameScriptWorker        = "script_worker"
	NameScriptShed          = "script_shed"
	NameScriptRun           = "script_run"
	NameScriptProgress      = "script_progress"
	NameThumbnailWorker     = "thumbnail_worker"
	NameThumbnailRender     = "thumbnail_render"
	NameWebhookCompactor    = "webhook_compactor"
	NameWebhookSegments     = "webhook_segments"
	NameWebhookSegmentWrite = "webhook_segment_write"
	NameWebhookCompaction   = "webhook_compaction"
	NameWebhookRetention    = "webhook_retention"
	NameWebhookViews        = "webhook_views"
	NameWebhookRefresh      = "webhook_source_refresh"
	NameWebhookStats        = "webhook_stats_flush"
	NameWebhookRawWindows   = "webhook_raw_windows"
	NameWebhookSpoolDrain   = "webhook_spool_drain"
	NameWebhookIMAPPoll     = "webhook_imap_poll"
	NameWebhookSpoolSegment = "webhook_spool_segment"
	NameMapFetch            = "map_fetch"
	NameMapFetchRegion      = "map_fetch_region"
	NameCatalogScan         = "catalog_scan"
	NameCallTaskHeartbeat   = "call_task_heartbeat"
	NameEndpointRefusals    = "endpoint_refusals_flush"

	// Alerts, keepalives and watchers.
	NameConnOAuthRefresh   = "connection_oauth_refresh"
	NameRevocationEscalate = "connection_revocation_escalator"
	NameReviewQueueAlert   = "review_queue_alert"
	NameWebhookAlert       = "webhook_alert"
	NameMemoryStaleness    = "memory_staleness"
	NameReloadBus          = "reload_bus"

	// LISTEN connections (Listen). The two pglisten channels report under
	// "listen_" + their channel name.
	NameListenIndexJobs = "listen_index_jobs"
	NameListenSessions  = "listen_session_broadcast"
	ListenPrefix        = "listen_"
)
