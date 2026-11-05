package scriptcontract

// LimitsNote is the help command's account of what bounds a run beside the
// numbers its limits list: how a draft differs from a platform run, what a
// tool a run calls keeps, how a refusal and an upstream's 429 are waited on,
// what a failed run's cause and retryable say, and the memory budget.
const LimitsNote = "A draft run is bounded more tightly than a platform run; the run_ limits are the ones a " +
	"saved script meets on this deployment. A tool a run calls keeps its own ceiling as well: " +
	"a trino_export or api_export inside a run is bounded by that tool's timeout. " +
	"The platform never runs a failed run again on its own. A rate-limit refusal of a " +
	"call is not a script error: the host waits the refusal's interval within the run's " +
	"deadline and issues the call again, and the wait is written to the run's log; an " +
	"upstream's 429 (upstream_retryable) is waited on the same way, at most 3 times. " +
	"A failed run carries cause (script, upstream, transient, memory, worker_lost, platform, " +
	"state_conflict) and retryable, which is true only when running it again is expected " +
	"to succeed. run_max_memory_bytes is the memory one run may hold " +
	"(scripts.worker.max_run_memory; 0 is no budget), measured at every host call over the " +
	"values the script can still reach. A page of rows costs far more once decoded than on the " +
	"wire: a 9 MB page of JSON rows allocates about 14 times its size while it is decoded and " +
	"holds about 4.5 times it afterwards, so page the work and export each page with " +
	"platform.export(..., append=True). A run over the budget fails with " +
	"cause memory and is not retried; run_draft and a run's metrics report " +
	"peak_memory_bytes."

// Overlap is the help command's account of a script's runs overlapping and
// of the exclusive setting that stops them.
const Overlap = "A script's runs may overlap unless it is set to run one at a time: a run_script run can " +
	"execute beside a scheduled one, and two run_script runs beside each other. Then the state check at " +
	"save time is the only guard, and it acts after the fact: the later run's side effects stand and it " +
	"fails with state_conflict, so its cursor is not saved and the next run repeats its work. Set " +
	"exclusive=\"refuse\" with update for a script whose runs must not overlap: while any run of it is pending " +
	"or running, a scheduled fire is recorded as skipped_overlap naming that run, and run_script and a " +
	"portal run are refused naming it, with nothing queued. Set exclusive=\"queue\" to have such a run " +
	"wait instead: it stays pending until no other run of the script is running. A schedule never overlaps itself either way."
