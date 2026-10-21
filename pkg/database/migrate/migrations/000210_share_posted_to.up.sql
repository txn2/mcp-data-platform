-- A link share created by posting an asset to a notification channel names
-- the channel it was posted to, so the owner's share list says where the link
-- went (#2241). Empty for every share that was not a channel post.
ALTER TABLE portal_shares ADD COLUMN IF NOT EXISTS posted_to TEXT NOT NULL DEFAULT '';
