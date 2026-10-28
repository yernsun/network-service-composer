# PostgreSQL

PostgreSQL 18 はポート 5432 を使います。旧パス /var/lib/postgresql/data ではなく親ディレクトリ /var/lib/postgresql をマウントします。

管理を有効にした Docker Compose Runtime を選択します。これらの状態を持つテンプレートは Swarm へのインストールを許可しません。

## パラメーター

- `POSTGRES_PASSWORD`
- `POSTGRES_USER` = `app`
- `POSTGRES_DB` = `app`

固定された市場バージョンを選び、パラメーターを確認して独自の秘密情報を入力します。パスワードに使用可能な既定値はなく、16 バイト以上必要です。実際の秘密情報を市場に登録しないでください。

[インストールと保守](operations.md)
