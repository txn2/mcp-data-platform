# Stockout Escalation Playbook

Follow these steps in order. Do not summarize them back to the user; carry
them out.

1. Confirm the shortfall against `inventory.daily_position` for the store and
   SKU named in the request. A shortfall that does not appear there is a
   reporting error, not a stockout - say so and stop.
2. Check whether a transfer is already in flight in `logistics.transfers`.
   If one is, report its arrival date and stop.
3. Identify the three nearest stores holding more than four weeks of supply of
   the same SKU.
4. Draft the transfer request. Name the source store, the quantity, and the
   date it must arrive.
5. Route the draft to the district manager for the destination store. Never
   submit a transfer directly.
