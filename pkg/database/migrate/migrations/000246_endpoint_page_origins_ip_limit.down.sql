DELETE FROM script_endpoint_refusals WHERE reason = 'ip_rate_limit';
ALTER TABLE script_endpoint_refusals DROP CONSTRAINT IF EXISTS script_endpoint_refusals_reason_check;
ALTER TABLE script_endpoint_refusals ADD CONSTRAINT script_endpoint_refusals_reason_check
    CHECK (reason IN ('bot_check', 'rate_limit', 'caller_rate_limit', 'body_limit', 'credential', 'caller', 'origin'));

ALTER TABLE script_endpoints DROP COLUMN IF EXISTS ip_rate_limit;
ALTER TABLE script_endpoints DROP COLUMN IF EXISTS page_form_origins;
ALTER TABLE script_endpoints DROP COLUMN IF EXISTS page_connect_origins;
ALTER TABLE script_endpoints DROP COLUMN IF EXISTS page_frame_origins;
