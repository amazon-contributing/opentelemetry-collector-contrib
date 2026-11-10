/* otel-collector-ignore */ SELECT plan FROM pg_store_plans WHERE queryid::text = $1 ORDER BY calls DESC LIMIT 1
