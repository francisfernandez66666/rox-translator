-- backup_tenants_20261023.sql
-- 删之前的安全网：把四个待删租户（2/5/6/8）在 46 张租户级表里的行，整行复制到
-- purge_bak_20261023 schema。表不存在则跳过。
-- 恢复方式（如事后发现删错）：
--   INSERT INTO public.<表> SELECT * FROM purge_bak_20261023.<表>;
--   INSERT INTO public.tenants SELECT * FROM purge_bak_20261023.tenants;
-- 确认无误后清理：DROP SCHEMA purge_bak_20261023 CASCADE;
\set ON_ERROR_STOP on
CREATE SCHEMA IF NOT EXISTS purge_bak_20261023;
DO $$
DECLARE
  tids bigint[] := ARRAY[2,5,6,8];
  tbls text[] := ARRAY[
    'users','orgs','orders','payments','invoices','usdt_orders','renewal_attempts',
    'balance_accounts','quota_grants','usage_ledger','usage_daily','coupon_redemptions',
    'registration_attribution','packages','kb_packages','kb_entries','kb_pack_grants',
    'kb_safety_phrases','kb_staged_entries','kb_upload_rewards','tickets','ticket_segments',
    'ticket_files','ticket_state','output_artifacts','tm_segments','tm_review','tm_hit_count',
    'translation_edits','chat_conversations','eval_records','feedbacks','user_task_claims',
    'user_task_rewards','user_trial_grants','webhooks','webhook_deliveries','scim_config',
    's7_watchlist','api_keys','invite_codes','notifications','alerts','jobs','system_config',
    'audit_logs'
  ];
  t text;
  n bigint;
  saved bigint;
BEGIN
  FOREACH t IN ARRAY tbls LOOP
    BEGIN
      EXECUTE format('DROP TABLE IF EXISTS purge_bak_20261023.%I', t);
      EXECUTE format(
        'CREATE TABLE purge_bak_20261023.%I AS SELECT * FROM public.%I WHERE tenant_id = ANY($1)', t, t)
        USING tids;
      GET DIAGNOSTICS n = ROW_COUNT;
      saved := saved + n;
      IF n > 0 THEN
        RAISE NOTICE '备份 %  rows=%', t, n;
      END IF;
    EXCEPTION WHEN undefined_table OR undefined_column THEN
      NULL;
    END;
  END LOOP;
  EXECUTE 'DROP TABLE IF EXISTS purge_bak_20261023.tenants';
  EXECUTE 'CREATE TABLE purge_bak_20261023.tenants AS SELECT * FROM public.tenants WHERE id = ANY($1)' USING tids;
  GET DIAGNOSTICS n = ROW_COUNT;
  RAISE NOTICE '备份 tenants  rows=%  合计业务行=%', n, saved;
END $$;
