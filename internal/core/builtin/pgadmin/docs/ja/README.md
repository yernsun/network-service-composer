# pgAdmin

pgAdmin はポート 80 で管理者メールとパスワードを使います。DATABASE_NETWORK に既存 PostgreSQL ネットワークを指定し、ログイン後に DB サーバーを手動登録します。PostgreSQL 自体は作成しません。データ: /var/lib/pgadmin。

管理を有効にした Docker Compose Runtime を選択します。これらの状態を持つテンプレートは Swarm へのインストールを許可しません。

## パラメーター

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

固定された市場バージョンを選び、パラメーターを確認して独自の秘密情報を入力します。パスワードに使用可能な既定値はなく、16 バイト以上必要です。実際の秘密情報を市場に登録しないでください。

[インストールと保守](operations.md)
