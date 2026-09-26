-- Gives messages_cache an absolute, batch-independent display order.
--
-- sort_rank is now each message's position from the top of the mailbox
-- (0 = newest), written by MessageCache.Upsert from the fetch window's
-- offset (offset+i), and MessageCache.list orders by it alone.
--
-- Before this, sort_rank was a per-batch rank (len(msgs)-i) and list
-- ordered by (fetched_at DESC, sort_rank DESC). Once the cache held two
-- pages fetched at different times, the newer batch floated above the
-- older one, so a served window came back interleaved (e.g. a page at
-- offset 0 then offset 20 then a served offset=0 window returned
-- [u20..u39, u0..u19]) and could even drop rows — see PLAN.md §10.9.
-- Existing rows carry the old per-batch values, which are meaningless
-- under the new rule, so both cache tables are cleared; the cache is
-- repopulated on the next fetch (it is a cache — see PRD.MD §6.3).
DELETE FROM messages_cache;
DELETE FROM messages_cache_state;
