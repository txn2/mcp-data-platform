-- 000244: a site's environments are stored in the order a release is
-- promoted through them, and every site has dev and prod (#2617).
--
-- dev is always first and prod always last; an environment between them is
-- placed when it is created and can be moved. Promotion goes one step: an
-- environment takes a release only from the one before it.
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS position INT NOT NULL DEFAULT 0;

-- Each site starts in the order the portal drew it by until now: dev first,
-- prod, production and live last, the rest by name between them.
UPDATE site_environments e SET position = o.n
  FROM (SELECT site_id, name,
               ROW_NUMBER() OVER (PARTITION BY site_id
                                  ORDER BY CASE WHEN name = 'dev' THEN 0
                                                WHEN name IN ('prod', 'production', 'live') THEN 2
                                                ELSE 1 END,
                                           name COLLATE "C") - 1 AS n
          FROM site_environments) o
 WHERE e.site_id = o.site_id AND e.name = o.name;

-- A site with no prod gets an empty one, last, with no release. Its main
-- address stays where it was.
INSERT INTO site_environments (site_id, name, updated_by, position)
SELECT s.id, 'prod', 'platform',
       COALESCE((SELECT MAX(x.position) + 1 FROM site_environments x WHERE x.site_id = s.id), 0)
  FROM sites s
 WHERE NOT EXISTS (SELECT 1 FROM site_environments e WHERE e.site_id = s.id AND e.name = 'prod');
