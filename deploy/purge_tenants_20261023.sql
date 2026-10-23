-- purge_tenants_20261023.sql
-- 清理四个测试租户：2 Hajimi / 5 UAT1015测试租户 / 6 比亚迪 / 8 zzfbaiyanhaha
-- 表清单与 backend-go/internal/tenant/scoped_tables.go 的 ScopedTables 逐字一致（46 张），
-- 由 `sed -n '/^var ScopedTables/,/^}/p' ... | grep -o '"[a-z_0-9]*"'` 生成，不手抄。
-- 语义与 store.PurgeTenantCascade 相同：逐表删、表不存在跳过、tenants 行最后删。
-- 整个 DO 块是一个事务：任何非「表不存在」的错误都会整体回滚，不留半途状态。
\set ON_ERROR_STOP on
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
  tid bigint;
  t text;
  n bigint;
  total bigint;
BEGIN
  FOREACH tid IN ARRAY tids LOOP
    total := 0;
    FOREACH t IN ARRAY tbls LOOP
      BEGIN
        EXECUTE format('DELETE FROM %I WHERE tenant_id = $1', t) USING tid;
        GET DIAGNOSTICS n = ROW_COUNT;
        IF n > 0 THEN
          RAISE NOTICE 'tid=%  %  rows=%', tid, t, n;
          total := total + n;
        END IF;
      -- 两种「这张表本来就不归 tenant_id 管」的形态都跳过：
      --   ① 表不存在（老库/本部署没这张表）；
      --   ② 表在但没有 tenant_id 列（现网 ticket_state 就是这种，它是全局状态表）。
      EXCEPTION WHEN undefined_table OR undefined_column THEN
        NULL;
      END;
    END LOOP;
    DELETE FROM tenants WHERE id = tid;
    GET DIAGNOSTICS n = ROW_COUNT;
    RAISE NOTICE 'tid=%  业务行合计=%  tenants行删除=%', tid, total, n;
  END LOOP;
END $$;
