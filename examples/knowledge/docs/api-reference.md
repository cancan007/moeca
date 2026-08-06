# 決済 API リファレンス

ベース URL: `https://api.example.internal/payments/v2`

## POST /payments

決済要求を受け付け、与信を確保する。

| ヘッダ | 必須 | 説明 |
|---|---|---|
| `Idempotency-Key` | 必須 | 同じキーの再送は最初の応答を返す。24 時間保持 |
| `Authorization` | 必須 | Bearer トークン |

リクエスト:

```json
{
  "amount": 12800,
  "currency": "JPY",
  "orderId": "ord_8813",
  "method": "card"
}
```

`amount` は最小通貨単位の整数。JPY なら円、USD ならセント。小数は受け付けない。

応答 201:

```json
{
  "paymentId": "pay_01HX...",
  "status": "authorized",
  "expiresAt": "2026-04-02T09:00:00Z"
}
```

`status` は `authorized` / `captured` / `failed` / `expired` のいずれか。
与信は 7 日で失効する。

## POST /payments/{id}/capture

与信済みの決済を確定する。金額の部分確定に対応する。

```json
{ "amount": 12800 }
```

`amount` を省略すると与信全額を確定する。与信額を超える確定は 422 になる。

## GET /payments/{id}

決済 1 件を取得する。台帳へ書き込み済みの場合のみ `ledgerEntryId` が入る。

## エラー

| コード | 意味 | 再試行 |
|---|---|---|
| 400 | 要求が不正 | しない |
| 409 | 冪等キーの競合(同じキーで内容が異なる) | しない |
| 422 | 金額や状態の前提を満たさない | しない |
| 429 | レート制限 | `Retry-After` に従う |
| 502 / 504 | PSP 側の障害 | 指数バックオフで再試行 |
