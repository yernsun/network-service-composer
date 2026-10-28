# MySQL

root とアプリ用のパスワードを分離した MySQL。ポート 3306、データ: /var/lib/mysql。

このバージョンは Docker Compose と Swarm Service をサポートします。Swarm では単一レプリカを選択したデータノードに固定し、ローカル名前付きボリュームと停止後に起動する更新を使用します。データノードがオフラインでも他のノードには移動しません。

## パラメーター

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE`
- `MYSQL_USER`

Ready/Active のデータノードを選択します。永続サービスは単一レプリカでこのノードに固定され、オフライン時は利用できません。

[Compose と Swarm のデプロイ](swarm-operations.md)
