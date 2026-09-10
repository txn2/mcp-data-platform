-- Restore the key script.RunOutput carried before #1666.
UPDATE script_runs
SET outputs = COALESCE((
        SELECT jsonb_agg(
            CASE
                WHEN elem ? 'table_changes'
                    THEN (elem - 'table_changes') || jsonb_build_object('tables', elem -> 'table_changes')
                ELSE elem
            END
            ORDER BY ord
        )
        FROM jsonb_array_elements(outputs) WITH ORDINALITY AS t(elem, ord)
    ), outputs)
WHERE jsonb_typeof(outputs) = 'array'
  AND EXISTS (SELECT 1 FROM jsonb_array_elements(outputs) AS e WHERE e ? 'table_changes');
