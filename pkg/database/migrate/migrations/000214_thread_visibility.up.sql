-- A feedback thread is internal or shared (#2227). People of an external
-- persona never see an internal thread; a thread one of them opens is shared.
-- Threads written before this are internal.
ALTER TABLE portal_threads ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'internal'
    CHECK (visibility IN ('internal', 'shared'));
