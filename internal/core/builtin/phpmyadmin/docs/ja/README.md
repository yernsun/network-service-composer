# phpMyAdmin

phpMyAdmin は DATABASE_NETWORK 経由で PMA_HOST と PMA_PORT に接続します。ポート 80 のログイン画面で既存 MySQL 認証情報を入力します。自動ログインや任意サーバー選択は無効です。セッション: /sessions。

管理を有効にした Docker Compose Runtime を選択します。これらの状態を持つテンプレートは Swarm へのインストールを許可しません。

## パラメーター

- `PMA_HOST`
- `PMA_PORT` = `3306`
- `DATABASE_NETWORK`

固定された市場バージョンを選び、パラメーターを確認して独自の秘密情報を入力します。パスワードに使用可能な既定値はなく、16 バイト以上必要です。実際の秘密情報を市場に登録しないでください。

[インストールと保守](operations.md)
