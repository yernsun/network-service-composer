# pgAdmin

pgAdmin はポート 80 で管理者メールとパスワードを使います。DATABASE_NETWORK に既存 PostgreSQL ネットワークを指定し、ログイン後に DB サーバーを手動登録します。PostgreSQL 自体は作成しません。データ: /var/lib/pgadmin。

このバージョンは Docker Compose と Swarm Service をサポートします。Swarm では単一レプリカを選択したデータノードに固定し、ローカル名前付きボリュームと停止後に起動する更新を使用します。データノードがオフラインでも他のノードには移動しません。

## パラメーター

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

Ready/Active のデータノードを選択します。永続サービスは単一レプリカでこのノードに固定され、オフライン時は利用できません。

[Compose と Swarm のデプロイ](swarm-operations.md)
