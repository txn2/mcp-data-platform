-- 000146: a catalog spec entry records the format it was supplied in
--
-- API catalogs accepted OpenAPI documents only. A SOAP upstream publishes a
-- WSDL, and a large share of ERP, finance, HR and logistics systems expose
-- their integration surface only that way (#1736).
--
-- Two columns rather than one, because a WSDL is not what the gateway serves:
--
--   spec_format     what the operator supplied. 'openapi' is the default so
--                   every existing row keeps its meaning without a backfill.
--   openapi_content the document the importer rendered from a non-OpenAPI
--                   source. Empty for an openapi spec, where `content` is
--                   already the effective document.
--
-- `content` keeps holding exactly what the operator supplied, so reading a
-- spec back returns the WSDL they wrote rather than a generated document they
-- have never seen, and the spec editor round-trips. Everything downstream
-- reads SpecEntry.Effective(), which prefers openapi_content.
ALTER TABLE api_catalog_specs
    ADD COLUMN spec_format     TEXT NOT NULL DEFAULT 'openapi',
    ADD COLUMN openapi_content TEXT NOT NULL DEFAULT '';

ALTER TABLE api_catalog_specs
    ADD CONSTRAINT api_catalog_specs_spec_format_check
        CHECK (spec_format IN ('openapi', 'wsdl'));
