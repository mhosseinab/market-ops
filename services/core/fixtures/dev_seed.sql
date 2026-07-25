-- Minimal dev fixture rows for local development (task db:reset).
-- Not a migration: never applied in production; deterministic IDs so local
-- tooling and screens can reference a known organization/account.
-- Idempotent: db:reset runs against a freshly created DB, but ON CONFLICT keeps
-- this safe to re-run against an existing dev DB.

INSERT INTO organizations (id, name)
VALUES ('00000000-0000-0000-0000-000000000001', 'Dev Organization')
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (id, organization_id, email, role)
VALUES ('00000000-0000-0000-0000-000000000002',
        '00000000-0000-0000-0000-000000000001',
        'owner@dev.local', 'owner')
ON CONFLICT (id) DO NOTHING;

INSERT INTO marketplace_accounts (id, organization_id, native_account_id, display_name)
VALUES ('00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-000000000001',
        'dev-dk-account', 'Dev DK Account')
ON CONFLICT (id) DO NOTHING;

-- ===========================================================================
-- Journey fixtures (issue #84 / the S32 duplicate-root expansion).
--
-- WHY THESE EXIST: the real-core Playwright journey gate
-- (apps/web/tests/e2e/journey{2,3,4}*.spec.ts, run by
-- tools/integration/run_killswitch_journey.sh) claims to exercise the daily
-- decision (journey 2), bulk invalidation/confirmation (journey 3), and the
-- blocker workflow (journey 4). Before this section the seed carried ONLY
-- org/user/account rows, so every one of those journeys found no event, no
-- card, and no candidate and passed through a conditional "nothing here"
-- branch — a gate that could not go red. These rows make each claimed path
-- REACHABLE, so the corresponding assertions can be unconditional and FAIL
-- when the behavior is absent.
--
-- Determinism: every id is fixed, so a test may deep-link an exact card. All
-- times are relative to now() so a fixture is LIVE (unexpired, fresh) at every
-- `task db:reset`, and the whole section is ON CONFLICT DO NOTHING idempotent.
--
-- Native id range: 9_000_00x, deliberately disjoint from the mockdk catalog
-- fixture (cmd/mockdk) that journey 1's connect→sync imports, so these rows and
-- the synced catalog never collide on (account, native id).
--
-- MONEY (PRD §9.1): every amount below is an exact (mantissa, currency,
-- exponent) triple — IRR, exponent 0. Raw marketplace price evidence stays
-- quarantined as verbatim text/value/unit and is never promoted to Money.
-- ===========================================================================

-- One product carrying both journey variants.
INSERT INTO products (id, marketplace_account_id, native_product_id, title, brand_title, product_url)
VALUES ('00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-000000000003',
        9000001, 'Journey Fixture Product', 'Journey Brand',
        'https://example.invalid/journey-product')
ON CONFLICT (id) DO NOTHING;

-- Variant A — the ACTIONABLE lane: verified evidence, complete margin
-- readiness, an approvable recommendation and a live approval control. Drives
-- journey 2 (Today → event → recommendation → structured approval) and is the
-- executable bulk candidate for journey 3.
INSERT INTO variants (id, marketplace_account_id, product_id, native_variant_id, native_product_id, supplier_code, title)
VALUES ('00000000-0000-0000-0000-0000000000b1',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000a1',
        9000011, 9000001, 'JRN-A', 'Journey Variant A (actionable)')
ON CONFLICT (id) DO NOTHING;

-- Variant B — the BLOCKED lane: unverified evidence, so its event is
-- non-actionable and Today must raise the data-readiness blocker banner.
-- Drives journey 4's blocker workflow.
INSERT INTO variants (id, marketplace_account_id, product_id, native_variant_id, native_product_id, supplier_code, title)
VALUES ('00000000-0000-0000-0000-0000000000b2',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000a1',
        9000012, 9000001, 'JRN-B', 'Journey Variant B (blocked)')
ON CONFLICT (id) DO NOTHING;

-- Variant C — a SECOND executable lane, dedicated to journey 3's bulk approval.
-- It exists so journey 3 never depends on journey 2's mutation: journey 2
-- CONFIRMS variant A's card, which removes A from the awaiting-confirmation
-- actions queue, so a bulk candidate sharing that card would stop being an
-- approvable selection member the moment journey 2 ran. With its own card,
-- journey 3's assertions hold regardless of file order.
INSERT INTO variants (id, marketplace_account_id, product_id, native_variant_id, native_product_id, supplier_code, title)
VALUES ('00000000-0000-0000-0000-0000000000b3',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000a1',
        9000013, 9000001, 'JRN-C', 'Journey Variant C (bulk candidate)')
ON CONFLICT (id) DO NOTHING;

-- Confirmed, active Market Product Identities (CAT-002). An observation target
-- REQUIRES one (OBS-001 is enforced by trg_observation_targets_confirmed), so
-- these are what make the bulk candidate set reachable at all.
INSERT INTO market_product_identities (id, marketplace_account_id, variant_id, native_variant_id, native_product_id, state, active, candidate_source, version)
VALUES ('00000000-0000-0000-0000-0000000000c1',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000b1',
        9000011, 9000001, 'confirmed', true, 'exact_native_id', 1)
ON CONFLICT (id) DO NOTHING;

INSERT INTO market_product_identities (id, marketplace_account_id, variant_id, native_variant_id, native_product_id, state, active, candidate_source, version)
VALUES ('00000000-0000-0000-0000-0000000000c2',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000b2',
        9000012, 9000001, 'confirmed', true, 'exact_native_id', 1)
ON CONFLICT (id) DO NOTHING;

INSERT INTO market_product_identities (id, marketplace_account_id, variant_id, native_variant_id, native_product_id, state, active, candidate_source, version)
VALUES ('00000000-0000-0000-0000-0000000000c3',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000b3',
        9000013, 9000001, 'confirmed', true, 'exact_native_id', 1)
ON CONFLICT (id) DO NOTHING;

-- Observation targets — the journey-3 candidate set (/observation/targets).
INSERT INTO observation_targets (id, marketplace_account_id, identity_id, variant_id, native_variant_id, native_product_id, tier, cadence_seconds, freshness_deadline_seconds, active)
VALUES ('00000000-0000-0000-0000-0000000000d1',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000c1',
        '00000000-0000-0000-0000-0000000000b1',
        9000011, 9000001, 'priority', 3600, 21600, true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO observation_targets (id, marketplace_account_id, identity_id, variant_id, native_variant_id, native_product_id, tier, cadence_seconds, freshness_deadline_seconds, active)
VALUES ('00000000-0000-0000-0000-0000000000d2',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000c2',
        '00000000-0000-0000-0000-0000000000b2',
        9000012, 9000001, 'standard', 21600, 86400, true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO observation_targets (id, marketplace_account_id, identity_id, variant_id, native_variant_id, native_product_id, tier, cadence_seconds, freshness_deadline_seconds, active)
VALUES ('00000000-0000-0000-0000-0000000000d3',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000c3',
        '00000000-0000-0000-0000-0000000000b3',
        9000013, 9000001, 'priority', 3600, 21600, true)
ON CONFLICT (id) DO NOTHING;

-- Append-only observation evidence backing each derived offer. Price stays RAW
-- (money quarantine): verbatim text, value token, and source unit — never Money.
INSERT INTO observations (id, captured_at, target_id, marketplace_account_id, native_variant_id, native_seller_id, offer_identity,
                          route, sub_route, parser_version, connector_version, source_url, source_type,
                          evidence_ref, raw_fixture_ref, price_raw_text, price_raw_value, price_raw_unit,
                          availability_status, stock_signal, quality, freshness_deadline, dedup_key,
                          schema_valid, identity_valid, confidence)
VALUES ('00000000-0000-0000-0000-0000000000e1',
        now() - interval '30 minutes',
        '00000000-0000-0000-0000-0000000000d1',
        '00000000-0000-0000-0000-000000000003',
        9000011, 'seller-journey-a', '9000011:seller-journey-a',
        'route_c', '', 'journey-fixture-v1', 'journey-fixture-v1',
        'https://example.invalid/journey-product', 'public-web-endpoint',
        'journey-fixture/observation/a', '', '14,350,000', '14350000', 'IRR',
        'in_stock', 12, 'verified', now() + interval '6 hours', 'journey-fixture-dedup-a',
        true, true, 'verified')
ON CONFLICT DO NOTHING;

INSERT INTO observations (id, captured_at, target_id, marketplace_account_id, native_variant_id, native_seller_id, offer_identity,
                          route, sub_route, parser_version, connector_version, source_url, source_type,
                          evidence_ref, raw_fixture_ref, price_raw_text, price_raw_value, price_raw_unit,
                          availability_status, stock_signal, quality, freshness_deadline, dedup_key,
                          schema_valid, identity_valid, confidence)
VALUES ('00000000-0000-0000-0000-0000000000e2',
        now() - interval '45 minutes',
        '00000000-0000-0000-0000-0000000000d2',
        '00000000-0000-0000-0000-000000000003',
        9000012, 'seller-journey-b', '9000012:seller-journey-b',
        'route_c', '', 'journey-fixture-v1', 'journey-fixture-v1',
        'https://example.invalid/journey-product', 'public-web-endpoint',
        'journey-fixture/observation/b', '', '9,900,000', '9900000', 'IRR',
        'in_stock', 4, 'unverified', now() + interval '24 hours', 'journey-fixture-dedup-b',
        true, true, 'unverified')
ON CONFLICT DO NOTHING;

INSERT INTO observations (id, captured_at, target_id, marketplace_account_id, native_variant_id, native_seller_id, offer_identity,
                          route, sub_route, parser_version, connector_version, source_url, source_type,
                          evidence_ref, raw_fixture_ref, price_raw_text, price_raw_value, price_raw_unit,
                          availability_status, stock_signal, quality, freshness_deadline, dedup_key,
                          schema_valid, identity_valid, confidence)
VALUES ('00000000-0000-0000-0000-0000000000e3',
        now() - interval '20 minutes',
        '00000000-0000-0000-0000-0000000000d3',
        '00000000-0000-0000-0000-000000000003',
        9000013, 'seller-journey-c', '9000013:seller-journey-c',
        'route_c', '', 'journey-fixture-v1', 'journey-fixture-v1',
        'https://example.invalid/journey-product', 'public-web-endpoint',
        'journey-fixture/observation/c', '', '21,500,000', '21500000', 'IRR',
        'in_stock', 8, 'verified', now() + interval '6 hours', 'journey-fixture-dedup-c',
        true, true, 'verified')
ON CONFLICT DO NOTHING;

-- Derived current observed offers (/observation/observed-offers). Quality is the
-- observed §10.3 state, carried as-is and never upgraded.
INSERT INTO observed_offers (id, target_id, marketplace_account_id, offer_identity, native_variant_id, native_seller_id,
                             price_raw_text, price_raw_value, price_raw_unit, availability_status, stock_signal,
                             quality, captured_at, freshness_deadline, routes, last_observation_id)
VALUES ('00000000-0000-0000-0000-0000000000f1',
        '00000000-0000-0000-0000-0000000000d1',
        '00000000-0000-0000-0000-000000000003',
        '9000011:seller-journey-a', 9000011, 'seller-journey-a',
        '14,350,000', '14350000', 'IRR', 'in_stock', 12,
        'verified', now() - interval '30 minutes', now() + interval '6 hours',
        '["route_c"]'::jsonb, '00000000-0000-0000-0000-0000000000e1')
ON CONFLICT (target_id, offer_identity) DO NOTHING;

INSERT INTO observed_offers (id, target_id, marketplace_account_id, offer_identity, native_variant_id, native_seller_id,
                             price_raw_text, price_raw_value, price_raw_unit, availability_status, stock_signal,
                             quality, captured_at, freshness_deadline, routes, last_observation_id)
VALUES ('00000000-0000-0000-0000-0000000000f2',
        '00000000-0000-0000-0000-0000000000d2',
        '00000000-0000-0000-0000-000000000003',
        '9000012:seller-journey-b', 9000012, 'seller-journey-b',
        '9,900,000', '9900000', 'IRR', 'in_stock', 4,
        'unverified', now() - interval '45 minutes', now() + interval '24 hours',
        '["route_c"]'::jsonb, '00000000-0000-0000-0000-0000000000e2')
ON CONFLICT (target_id, offer_identity) DO NOTHING;

INSERT INTO observed_offers (id, target_id, marketplace_account_id, offer_identity, native_variant_id, native_seller_id,
                             price_raw_text, price_raw_value, price_raw_unit, availability_status, stock_signal,
                             quality, captured_at, freshness_deadline, routes, last_observation_id)
VALUES ('00000000-0000-0000-0000-0000000000f3',
        '00000000-0000-0000-0000-0000000000d3',
        '00000000-0000-0000-0000-000000000003',
        '9000013:seller-journey-c', 9000013, 'seller-journey-c',
        '21,500,000', '21500000', 'IRR', 'in_stock', 8,
        'verified', now() - interval '20 minutes', now() + interval '6 hours',
        '["route_c"]'::jsonb, '00000000-0000-0000-0000-0000000000e3')
ON CONFLICT (target_id, offer_identity) DO NOTHING;

-- Cost profiles (CST-002) — the ACTUAL driver of margin readiness: /cost/readiness
-- DERIVES the verdict from the in-force cost components, it does not read the
-- margin_readiness projection. Variant A therefore needs both hard-required
-- components, and commission must carry AUTHORITATIVE (connector) provenance —
-- a seller-entered commission is preserved as evidence but never satisfies the
-- requirement (§9.2/§16), so without this the variant would derive `missing`.
-- Variant B is deliberately given NO cost profile, so it derives `missing`: a
-- genuine, server-derived cost blocker rather than a hardcoded label.
INSERT INTO cost_profiles (id, marketplace_account_id, variant_id, component, version,
                           amount_mantissa, amount_currency, amount_exponent,
                           raw_text, raw_value, raw_unit, effective_from, stale_after, source)
VALUES ('00000000-0000-0000-0000-000000000401',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000b1',
        'cogs', 1, 9800000, 'IRR', 0,
        '9,800,000', '9800000', 'IRR', now() - interval '7 days', now() + interval '180 days', 'csv_import')
ON CONFLICT (id) DO NOTHING;

INSERT INTO cost_profiles (id, marketplace_account_id, variant_id, component, version,
                           amount_mantissa, amount_currency, amount_exponent,
                           raw_text, raw_value, raw_unit, effective_from, stale_after, source)
VALUES ('00000000-0000-0000-0000-000000000402',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000b1',
        'commission', 1, 1300000, 'IRR', 0,
        '1,300,000', '1300000', 'IRR', now() - interval '7 days', now() + interval '180 days', 'connector')
ON CONFLICT (id) DO NOTHING;

INSERT INTO cost_profiles (id, marketplace_account_id, variant_id, component, version,
                           amount_mantissa, amount_currency, amount_exponent,
                           raw_text, raw_value, raw_unit, effective_from, stale_after, source)
VALUES ('00000000-0000-0000-0000-000000000403',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000b3',
        'cogs', 1, 15200000, 'IRR', 0,
        '15,200,000', '15200000', 'IRR', now() - interval '7 days', now() + interval '180 days', 'csv_import')
ON CONFLICT (id) DO NOTHING;

INSERT INTO cost_profiles (id, marketplace_account_id, variant_id, component, version,
                           amount_mantissa, amount_currency, amount_exponent,
                           raw_text, raw_value, raw_unit, effective_from, stale_after, source)
VALUES ('00000000-0000-0000-0000-000000000404',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000b3',
        'commission', 1, 1900000, 'IRR', 0,
        '1,900,000', '1900000', 'IRR', now() - interval '7 days', now() + interval '180 days', 'connector')
ON CONFLICT (id) DO NOTHING;

-- Margin readiness (CST-003) projection, kept CONSISTENT with what the cost
-- profiles above derive: variant A `complete` (the only executable readiness),
-- variant B `missing`. The projection is a cache of the derivation, never a
-- substitute for it.
INSERT INTO margin_readiness (variant_id, marketplace_account_id, state, missing_components, stale_components)
VALUES ('00000000-0000-0000-0000-0000000000b1',
        '00000000-0000-0000-0000-000000000003',
        'complete', '[]'::jsonb, '[]'::jsonb)
ON CONFLICT (variant_id) DO NOTHING;

INSERT INTO margin_readiness (variant_id, marketplace_account_id, state, missing_components, stale_components)
VALUES ('00000000-0000-0000-0000-0000000000b2',
        '00000000-0000-0000-0000-000000000003',
        'missing', '["unit_cost"]'::jsonb, '[]'::jsonb)
ON CONFLICT (variant_id) DO NOTHING;

INSERT INTO margin_readiness (variant_id, marketplace_account_id, state, missing_components, stale_components)
VALUES ('00000000-0000-0000-0000-0000000000b3',
        '00000000-0000-0000-0000-000000000003',
        'complete', '[]'::jsonb, '[]'::jsonb)
ON CONFLICT (variant_id) DO NOTHING;

-- Market events — the Today ranked queue (EVT-004). Exactly TWO events are
-- seeded (variants A and B); variant C carries no event, so the Today queue is
-- deterministic while the bulk candidate set is not.
-- Event A: VERIFIED evidence ⇒ actionable ⇒ EventRow renders the "review" CTA,
-- which is journey 2's entry into the event detail.
INSERT INTO market_events (id, marketplace_account_id, variant_id, target_id, event_type, severity, state, dedup_key,
                           exposure_known, exposure_mantissa, exposure_currency, exposure_exponent,
                           confidence_bp, urgency_bp, evidence_observation_id, evidence_quality, evidence_ref,
                           evidence_detail, first_detected_at, last_evidence_at, expires_at)
VALUES ('00000000-0000-0000-0000-000000000201',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000b1',
        '00000000-0000-0000-0000-0000000000d1',
        'competitor_price', 'critical', 'open', 'journey-fixture-event-a',
        true, 8900000, 'IRR', 0,
        9000, 8500,
        '00000000-0000-0000-0000-0000000000e1', 'verified', 'journey-fixture/observation/a',
        '{"before": {"text": "15,000,000", "value": "15000000", "unit": "IRR"}, "after": {"text": "14,350,000", "value": "14350000", "unit": "IRR"}}'::jsonb,
        now() - interval '30 minutes', now() - interval '30 minutes', now() + interval '30 days')
ON CONFLICT (id) DO NOTHING;

-- Event B: UNVERIFIED evidence ⇒ NON-actionable ⇒ EventRow renders the blocked
-- panel and Today raises the data-readiness blocker banner. This is journey 4's
-- blocker workflow, which must remain fully reachable with the LLM plane down
-- (screens-only fallback, CHAT-009).
INSERT INTO market_events (id, marketplace_account_id, variant_id, target_id, event_type, severity, state, dedup_key,
                           exposure_known, exposure_mantissa, exposure_currency, exposure_exponent,
                           confidence_bp, urgency_bp, evidence_observation_id, evidence_quality, evidence_ref,
                           evidence_detail, first_detected_at, last_evidence_at, expires_at)
VALUES ('00000000-0000-0000-0000-000000000202',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000b2',
        '00000000-0000-0000-0000-0000000000d2',
        'seller_count', 'warning', 'open', 'journey-fixture-event-b',
        false, NULL, '', 0,
        4000, 3000,
        '00000000-0000-0000-0000-0000000000e2', 'unverified', 'journey-fixture/observation/b',
        '{"before": {"sellerCount": 3}, "after": {"sellerCount": 5}}'::jsonb,
        now() - interval '45 minutes', now() - interval '45 minutes', now() + interval '30 days')
ON CONFLICT (id) DO NOTHING;

-- The approvable PRC-001 recommendation for variant A. approvable=true is only
-- legal with complete readiness, no simulation and an expiry (rec_approvable_
-- requires_complete), so this row is a genuine executable recommendation.
INSERT INTO recommendations (id, marketplace_account_id, variant_id, lineage_id, version, event_id, objective,
                             current_price_mantissa, current_price_currency, current_price_exponent,
                             proposed_price_available, proposed_price_mantissa, proposed_price_currency, proposed_price_exponent,
                             current_contribution_available, current_contribution_mantissa, current_contribution_currency, current_contribution_exponent,
                             proposed_contribution_available, proposed_contribution_mantissa, proposed_contribution_currency, proposed_contribution_exponent,
                             allowed_range_available, allowed_range_min_mantissa, allowed_range_max_mantissa, allowed_range_currency, allowed_range_exponent,
                             readiness, evidence_quality, evidence_observation_id, evidence_refs, evidence_as_of,
                             cost_profile_version, policy_version, context_version, parameter_version,
                             inputs, assumptions, blockers, approvable, simulation, expires_at)
VALUES ('00000000-0000-0000-0000-000000000301',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000b1',
        '00000000-0000-0000-0000-000000000311', 1,
        '00000000-0000-0000-0000-000000000201', 'maximize_contribution',
        15000000, 'IRR', 0,
        true, 14200000, 'IRR', 0,
        true, 2100000, 'IRR', 0,
        true, 2450000, 'IRR', 0,
        true, 13000000, 16000000, 'IRR', 0,
        'complete', 'verified', '00000000-0000-0000-0000-0000000000e1',
        '["journey-fixture/observation/a"]'::jsonb, now() - interval '30 minutes',
        1, 1, 1, 1,
        -- `inputs` carries the contribution DEDUCTION breakdown ([]margin.Deduction).
        -- It is left EMPTY deliberately: money.Money has unexported fields and no
        -- MarshalJSON, so a deduction written here would round-trip with a ZERO
        -- Amount, i.e. a fabricated money value on a money path (§9.1/§4.6). An
        -- honest "no breakdown recorded" is correct; the surface renders the
        -- breakdown as unavailable rather than as zero.
        '[]'::jsonb,
        '["stock level stable over the observation window"]'::jsonb,
        '[]'::jsonb,
        true, false, now() + interval '30 days')
ON CONFLICT (id) DO NOTHING;

-- The approvable PRC-001 recommendation for variant C (journey 3's bulk member).
-- Not event-driven: event_id is explicitly NULL, which the schema allows and the
-- contract renders as "not event-driven" rather than as a missing link.
INSERT INTO recommendations (id, marketplace_account_id, variant_id, lineage_id, version, event_id, objective,
                             current_price_mantissa, current_price_currency, current_price_exponent,
                             proposed_price_available, proposed_price_mantissa, proposed_price_currency, proposed_price_exponent,
                             current_contribution_available, current_contribution_mantissa, current_contribution_currency, current_contribution_exponent,
                             proposed_contribution_available, proposed_contribution_mantissa, proposed_contribution_currency, proposed_contribution_exponent,
                             allowed_range_available, allowed_range_min_mantissa, allowed_range_max_mantissa, allowed_range_currency, allowed_range_exponent,
                             readiness, evidence_quality, evidence_observation_id, evidence_refs, evidence_as_of,
                             cost_profile_version, policy_version, context_version, parameter_version,
                             inputs, assumptions, blockers, approvable, simulation, expires_at)
VALUES ('00000000-0000-0000-0000-000000000302',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-0000000000b3',
        '00000000-0000-0000-0000-000000000312', 1,
        NULL, 'maximize_contribution',
        22000000, 'IRR', 0,
        true, 21300000, 'IRR', 0,
        true, 4900000, 'IRR', 0,
        true, 5300000, 'IRR', 0,
        true, 20000000, 24000000, 'IRR', 0,
        'complete', 'verified', '00000000-0000-0000-0000-0000000000e3',
        '["journey-fixture/observation/c"]'::jsonb, now() - interval '20 minutes',
        1, 1, 1, 1,
        '[]'::jsonb,
        '["stock level stable over the observation window"]'::jsonb,
        '[]'::jsonb,
        true, false, now() + interval '30 days')
ON CONFLICT (id) DO NOTHING;

-- The APR-001 version-bound approval control for that recommendation.
-- state='awaiting_confirmation' is what mints a LIVE control (hasControl), so
-- journey 2 can drive a real structured confirmation, and journey 3 can build a
-- real (variantId, recommendationId) selection member from the actions queue.
INSERT INTO approval_cards (id, recommendation_id, marketplace_account_id, lineage_id, version,
                            action_id, parameter_version, context_version, policy_version, cost_profile_version,
                            evidence_versions, idempotency_key, state,
                            price_mantissa, price_currency, price_exponent, expires_at)
VALUES ('00000000-0000-0000-0000-000000000321',
        '00000000-0000-0000-0000-000000000301',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-000000000331', 1,
        '00000000-0000-0000-0000-000000000341', 1, 1, 1, 1,
        '{"00000000-0000-0000-0000-0000000000e1": 1}'::jsonb,
        'journey-fixture-idempotency-key-a', 'awaiting_confirmation',
        14200000, 'IRR', 0, now() + interval '30 days')
ON CONFLICT (id) DO NOTHING;

-- Append-only §8.4 state history for that card (draft → ready_for_review →
-- awaiting_confirmation), so the lifecycle is reconstructable for audit from
-- state rows alone rather than implied by the current-state column.
INSERT INTO approval_card_states (id, card_id, card_version, from_state, to_state, reason)
VALUES ('00000000-0000-0000-0000-000000000351',
        '00000000-0000-0000-0000-000000000321', 1, NULL, 'draft', 'journey fixture')
ON CONFLICT (id) DO NOTHING;

INSERT INTO approval_card_states (id, card_id, card_version, from_state, to_state, reason)
VALUES ('00000000-0000-0000-0000-000000000352',
        '00000000-0000-0000-0000-000000000321', 1, 'draft', 'ready_for_review', 'journey fixture')
ON CONFLICT (id) DO NOTHING;

INSERT INTO approval_card_states (id, card_id, card_version, from_state, to_state, reason)
VALUES ('00000000-0000-0000-0000-000000000353',
        '00000000-0000-0000-0000-000000000321', 1, 'ready_for_review', 'awaiting_confirmation', 'journey fixture')
ON CONFLICT (id) DO NOTHING;

-- Variant C's live approval control — journey 3's bulk selection member.
INSERT INTO approval_cards (id, recommendation_id, marketplace_account_id, lineage_id, version,
                            action_id, parameter_version, context_version, policy_version, cost_profile_version,
                            evidence_versions, idempotency_key, state,
                            price_mantissa, price_currency, price_exponent, expires_at)
VALUES ('00000000-0000-0000-0000-000000000322',
        '00000000-0000-0000-0000-000000000302',
        '00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-000000000332', 1,
        '00000000-0000-0000-0000-000000000342', 1, 1, 1, 1,
        '{"00000000-0000-0000-0000-0000000000e3": 1}'::jsonb,
        'journey-fixture-idempotency-key-c', 'awaiting_confirmation',
        21300000, 'IRR', 0, now() + interval '30 days')
ON CONFLICT (id) DO NOTHING;

INSERT INTO approval_card_states (id, card_id, card_version, from_state, to_state, reason)
VALUES ('00000000-0000-0000-0000-000000000361',
        '00000000-0000-0000-0000-000000000322', 1, NULL, 'draft', 'journey fixture')
ON CONFLICT (id) DO NOTHING;

INSERT INTO approval_card_states (id, card_id, card_version, from_state, to_state, reason)
VALUES ('00000000-0000-0000-0000-000000000362',
        '00000000-0000-0000-0000-000000000322', 1, 'draft', 'ready_for_review', 'journey fixture')
ON CONFLICT (id) DO NOTHING;

INSERT INTO approval_card_states (id, card_id, card_version, from_state, to_state, reason)
VALUES ('00000000-0000-0000-0000-000000000363',
        '00000000-0000-0000-0000-000000000322', 1, 'ready_for_review', 'awaiting_confirmation', 'journey fixture')
ON CONFLICT (id) DO NOTHING;
