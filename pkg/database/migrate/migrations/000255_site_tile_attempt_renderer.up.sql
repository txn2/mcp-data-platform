-- #2681: a site tile's attempts are counted per release AND per renderer
-- generation. Before, they were counted per release alone, so an environment
-- that spent its attempts under an older tile rule was never claimed again
-- after the rule was fixed, until a new release moved it. attempt_renderer is
-- the generation the attempts were made under; an older one starts over.
ALTER TABLE site_tiles ADD COLUMN IF NOT EXISTS attempt_renderer INT NOT NULL DEFAULT 0;
