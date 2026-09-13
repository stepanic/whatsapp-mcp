#!/usr/bin/env python3
"""Popuni captione koji su izgubljeni dok ih bridge nije citao.

`extractTextContent()` je do 13.09.2026. citao samo `Conversation` i
`ExtendedTextMessage`, pa je svaka slika/video/dokument s captionom zavrsila u
`messages.db` s praznim `content`. Popravak u `main.go` vrijedi od tog trenutka
nadalje; sve sto je vec u bazi ostaje prazno dok ga netko ne popuni.

Izvor istine je WhatsAppova vlastita baza, u kojoj caption stoji u
`ZWAMEDIAITEM.ZTITLE` (**ne** u `ZWAMESSAGE.ZTEXT` — taj je uz medijsku poruku
prazan). Spaja se preko `ZWAMESSAGE.ZSTANZAID`, sto je isti ID koji bridge
sprema u `messages.id`, pa je spoj tocan, a ne pogodak po vremenu.

Dry-run je default; pise se tek uz `--execute`. Drugi run je no-op jer se dira
iskljucivo red kojem je `content` prazan.
"""

from __future__ import annotations

import argparse
import shutil
import sqlite3
import sys
import tempfile
from pathlib import Path

CHAT_STORAGE = Path.home() / (
    "Library/Group Containers/group.net.whatsapp.WhatsApp.shared/ChatStorage.sqlite"
)
MESSAGES_DB = Path.home() / "git/mcps/whatsapp-mcp/whatsapp-bridge/store/messages.db"


def ucitaj_captione(izvor: Path, radni: Path) -> dict[str, str]:
    """Preslikaj ChatStorage pa ga procitaj.

    Kopiraju se i `-wal` i `-shm`: bez njih se cita zadnji checkpoint, koji zna
    biti star mjesecima, i skripta tiho ne nade najnovije poruke.
    """
    shutil.copy2(izvor, radni)
    for pratilac in ("-wal", "-shm"):
        prati = izvor.with_name(izvor.name + pratilac)
        if prati.exists():
            shutil.copy2(prati, radni.with_name(radni.name + pratilac))

    veza = sqlite3.connect(f"file:{radni}?mode=ro", uri=True)
    try:
        redovi = veza.execute(
            """
            SELECT m.ZSTANZAID, mi.ZTITLE
              FROM ZWAMESSAGE m
              JOIN ZWAMEDIAITEM mi ON mi.Z_PK = m.ZMEDIAITEM
             WHERE m.ZSTANZAID IS NOT NULL
               AND mi.ZTITLE IS NOT NULL
               AND trim(mi.ZTITLE) != ''
               -- Samo tipovi kod kojih je ZTITLE doista caption: 1 slika,
               -- 2 video, 8 dokument, 11 GIF. Kod tipa 7 (link preview) ZTITLE
               -- je naslov strane, a kod 14 su goli hex ID-evi -- oboje bi se
               -- upisalo kao da je covjek to napisao.
               AND m.ZMESSAGETYPE IN (1, 2, 8, 11)
            """
        ).fetchall()
    finally:
        veza.close()

    return {stanza: naslov for stanza, naslov in redovi}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--execute", action="store_true", help="stvarno upisi (default je dry-run)")
    parser.add_argument("--messages-db", type=Path, default=MESSAGES_DB)
    parser.add_argument("--chat-storage", type=Path, default=CHAT_STORAGE)
    args = parser.parse_args()

    if not args.chat_storage.exists():
        print(f"GRESKA: nema {args.chat_storage}", file=sys.stderr)
        return 1
    if not args.messages_db.exists():
        print(f"GRESKA: nema {args.messages_db}", file=sys.stderr)
        return 1

    with tempfile.TemporaryDirectory() as tmp:
        captioni = ucitaj_captione(args.chat_storage, Path(tmp) / "ChatStorage.sqlite")
    print(f"ChatStorage: {len(captioni)} medijskih poruka ima caption")

    veza = sqlite3.connect(args.messages_db)
    try:
        prazni = veza.execute(
            """
            SELECT id, chat_jid, media_type
              FROM messages
             WHERE media_type IS NOT NULL AND media_type != ''
               AND (content IS NULL OR content = '')
            """
        ).fetchall()
        print(f"messages.db: {len(prazni)} medijskih poruka bez teksta")

        poklapanja = [
            (captioni[mid], mid, chat)
            for mid, chat, _tip in prazni
            if mid in captioni
        ]
        print(f"poklapanja po ID-u: {len(poklapanja)}")

        for caption, mid, _chat in poklapanja[:5]:
            print(f"  {mid[:16]}…  {caption[:70]!r}")

        if not args.execute:
            print("\ndry-run — nista nije upisano. Za upis: --execute")
            return 0

        # Jedna transakcija: polupopunjena baza je gora od nepopunjene.
        with veza:
            veza.executemany(
                "UPDATE messages SET content = ? "
                " WHERE id = ? AND chat_jid = ? AND (content IS NULL OR content = '')",
                poklapanja,
            )
        print(f"\nupisano: {len(poklapanja)}")
    finally:
        veza.close()

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
