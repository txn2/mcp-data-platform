-- #1666 gave the tables registered over a stored file one name per use:
-- `tables` for the rows a caller queries, `table_registrations` for the
-- maintenance view, and `table_changes` for the sentences a write reports
-- about what it did to those tables.
--
-- script.RunOutput carries the last of the three, and it is stored as well as
-- served: script_runs.outputs is a JSONB array of RunOutput, unmarshalled back
-- into the same struct. Renaming the field's tag alone would leave every run
-- already recorded reading back with no table report at all -- and a scheduled
-- script's run history is exactly where a run that put a table behind its file
-- has to say so.
--
-- Rewrite the key in place on every recorded output, preserving call order,
-- and leave every other field of the object as it is.
UPDATE script_runs
SET outputs = COALESCE((
        SELECT jsonb_agg(
            CASE
                WHEN elem ? 'tables'
                    THEN (elem - 'tables') || jsonb_build_object('table_changes', elem -> 'tables')
                ELSE elem
            END
            ORDER BY ord
        )
        FROM jsonb_array_elements(outputs) WITH ORDINALITY AS t(elem, ord)
    ), outputs)
WHERE jsonb_typeof(outputs) = 'array'
  AND EXISTS (SELECT 1 FROM jsonb_array_elements(outputs) AS e WHERE e ? 'tables');
