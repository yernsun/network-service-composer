# PostgreSQL

PostgreSQL 18 はポート 5432 を使います。旧パス /var/lib/postgresql/data ではなく親ディレクトリ /var/lib/postgresql をマウントします。

このバージョンは Docker Compose と Swarm Service をサポートします。Swarm では単一レプリカを選択したデータノードに固定し、ローカル名前付きボリュームと停止後に起動する更新を使用します。データノードがオフラインでも他のノードには移動しません。

## パラメーター

- `POSTGRES_PASSWORD`
- `POSTGRES_USER`
- `POSTGRES_DB`

Ready/Active のデータノードを選択します。永続サービスは単一レプリカでこのノードに固定され、オフライン時は利用できません。

[Compose と Swarm のデプロイ](swarm-operations.md)
