# Redis

AOF 永続化付き Redis キャッシュ。6379 番ポートで認証します。データ: /data。

このバージョンは Docker Compose と Swarm Service をサポートします。Swarm では単一レプリカを選択したデータノードに固定し、ローカル名前付きボリュームと停止後に起動する更新を使用します。データノードがオフラインでも他のノードには移動しません。

## パラメーター

- `REDIS_PASSWORD`

Ready/Active のデータノードを選択します。永続サービスは単一レプリカでこのノードに固定され、オフライン時は利用できません。

[Compose と Swarm のデプロイ](swarm-operations.md)
