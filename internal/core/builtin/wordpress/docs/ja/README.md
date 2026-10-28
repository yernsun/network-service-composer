# WordPress

WordPress には専用 MySQL が含まれます。DB の準備完了後にポート 80 のインストーラーを開きます。database-data と wordpress-data の両ボリュームを保持します。

管理を有効にした Docker Compose Runtime を選択します。これらの状態を持つテンプレートは Swarm へのインストールを許可しません。

## パラメーター

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE` = `app`
- `MYSQL_USER` = `app`

固定された市場バージョンを選び、パラメーターを確認して独自の秘密情報を入力します。パスワードに使用可能な既定値はなく、16 バイト以上必要です。実際の秘密情報を市場に登録しないでください。

[インストールと保守](operations.md)
