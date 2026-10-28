# phpMyAdmin

phpMyAdmin は DATABASE_NETWORK 経由で PMA_HOST と PMA_PORT に接続します。ポート 80 のログイン画面で既存 MySQL 認証情報を入力します。自動ログインや任意サーバー選択は無効です。セッション: /sessions。

このバージョンは Docker Compose と Swarm Service をサポートします。Swarm では単一レプリカを選択したデータノードに固定し、ローカル名前付きボリュームと停止後に起動する更新を使用します。データノードがオフラインでも他のノードには移動しません。

## パラメーター

- `PMA_HOST`
- `PMA_PORT`
- `DATABASE_NETWORK`

Ready/Active のデータノードを選択します。永続サービスは単一レプリカでこのノードに固定され、オフライン時は利用できません。

[Compose と Swarm のデプロイ](swarm-operations.md)
