# このホストでの稼働状態

2026-10-30に導入。

- サービス: ユーザーsystemd `light-server.service`
- 実行ファイル: `/mnt/ssd01/project/light-server/bin/light-server`
- 待ち受け: `127.0.0.1:8787`
- 保存先: `/home/yamagishi/.local/share/light-server/projects/<id>/`
- 設定: `/home/yamagishi/.config/systemd/user/light-server.service`
- 自動起動enabled、linger=yes。OS再起動試験は未実施。
- demo: `/mnt/ssd01/project/light-server/examples/demo` を登録済み。動作検証用のメモを保持。

更新は別名へbuildしてrenameした後、`systemctl --user restart light-server.service`。

検証: race付き全テスト、vet、HTTP CRUD/検索、16MiB転送ハッシュ、Range、プロセス再起動後保持、ブラウザ内JavaScript保存。停止した転送で管理操作が全体をブロックする問題は回帰テストで再現後修正した。
