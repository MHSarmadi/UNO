const I18N = {
	fa: {
		ping: "پینگ",
		ms: "میلی‌ثانیه",
		connected: "متصل",
		disconnected: "قطع",
		connecting: "در حال اتصال...",

		nickname: "نام مستعار",
		nicknamePlaceholder: "نام خود را وارد کنید",
		saveNickname: "ذخیره نام",

		createChallenge: "ساخت چالش",
		passwordOptional: "رمز (اختیاری)",
		password: "رمز عبور",
		initialCards: "تعداد کارت اولیه",
		create: "ایجاد",

		pendingChallenges: "چالش‌های در انتظار",
		refresh: "به‌روزرسانی",
		noChallenges: "هیچ چالشی وجود ندارد.",
		join: "پیوستن",

		lobby: "لابی",
		leave: "ترک",
		kick: "اخراج",
		waiting: "در انتظار بازیکنان...",
		startGame: "شروع بازی",
		players: "بازیکنان",
		you: "شما",

		yourTurn: "نوبت شماست",
		draw: "کشیدن کارت",
		pass: "پاس",
		claimStack: "برداشتن جریمه",
		activeColor: "رنگ فعال",
		directionClockwise: "ساعتگرد",
		directionCounterClockwise: "پادساعتگرد",

		chooseColor: "انتخاب رنگ",
		chooseTarget: "انتخاب بازیکن",
		cancel: "انصراف",
		confirm: "تایید",

		winner: "برنده",
		backToHome: "بازگشت به صفحه اصلی",

		color_red: "قرمز",
		color_yellow: "زرد",
		color_green: "سبز",
		color_blue: "آبی",
		color_wild: "وایلد",

		invalid_nickname: "نام مستعار نامعتبر است.",
		need_nickname: "ابتدا نام مستعار را تنظیم کنید.",
		invalid_room: "اتاق معتبر نیست.",
		wrong_password: "رمز عبور اشتباه است.",
		room_full: "اتاق پر است.",
		not_creator: "فقط سازنده می‌تواند این کار را انجام دهد.",
		invalid_settings: "تنظیمات نامعتبر است.",
		not_enough_players: "تعداد بازیکنان کافی نیست.",
		invalid_play: "این حرکت نامعتبر است.",
		invalid_target: "هدف نامعتبر است.",
		invalid_color: "رنگ نامعتبر است.",
		already_drew: "قبلاً در این نوبت کارت کشیده‌اید.",
		draw_first: "ابتدا یک کارت بکشید.",
		not_in_room: "در اتاق نیستید.",
		not_your_turn: "نوبت شما نیست.",
		unknown_message: "پیام ناشناخته.",
		internal_error: "خطای داخلی سرور.",
	},

	en: {
		ping: "Ping",
		ms: "ms",
		connected: "Connected",
		disconnected: "Disconnected",
		connecting: "Connecting...",

		nickname: "Nickname",
		nicknamePlaceholder: "Enter your nickname",
		saveNickname: "Save nickname",

		createChallenge: "Create challenge",
		passwordOptional: "Password (optional)",
		password: "Password",
		initialCards: "Initial cards",
		create: "Create",

		pendingChallenges: "Pending challenges",
		refresh: "Refresh",
		noChallenges: "No challenges available.",
		join: "Join",

		lobby: "Lobby",
		leave: "Leave",
		kick: "Kick",
		waiting: "Waiting for players...",
		startGame: "Start game",
		players: "Players",
		you: "You",

		yourTurn: "Your turn",
		draw: "Draw card",
		pass: "Pass",
		claimStack: "Claim stack",
		activeColor: "Active color",
		directionClockwise: "Clockwise",
		directionCounterClockwise: "Counterclockwise",

		chooseColor: "Choose color",
		chooseTarget: "Choose player",
		cancel: "Cancel",
		confirm: "Confirm",

		winner: "Winner",
		backToHome: "Back to home",

		color_red: "Red",
		color_yellow: "Yellow",
		color_green: "Green",
		color_blue: "Blue",
		color_wild: "Wild",

		invalid_nickname: "Invalid nickname.",
		need_nickname: "Set a nickname first.",
		invalid_room: "Invalid room.",
		wrong_password: "Wrong password.",
		room_full: "Room is full.",
		not_creator: "Only the creator can do this.",
		invalid_settings: "Invalid settings.",
		not_enough_players: "Not enough players.",
		invalid_play: "Invalid play.",
		invalid_target: "Invalid target.",
		invalid_color: "Invalid color.",
		already_drew: "You already drew this turn.",
		draw_first: "Draw a card first.",
		not_in_room: "You are not in a room.",
		not_your_turn: "Not your turn.",
		unknown_message: "Unknown message.",
		internal_error: "Internal server error.",
	},
};

function getToken() {
	let token = sessionStorage.getItem("uno_token");
	if (!token) {
		token = crypto.randomUUID
			? crypto.randomUUID()
			: Date.now().toString(36) + Math.random().toString(36).slice(2);
		sessionStorage.setItem("uno_token", token);
	}
	return token;
}

const { createApp } = Vue;

createApp({
	data() {
		return {
			locale: localStorage.getItem("uno_locale") || "fa",

			connected: false,
			ws: null,
			pingTimer: null,
			ping: null,

			playerId: "",
			nickname: "",
			nicknameInput: "",

			challenges: [],

			room: null,
			game: null,
			hand: [],
			gameOver: null,

			modal: null,
			selectedChallenge: null,
			passwordInput: "",

			createPassword: "",
			createCards: 7,

			toasts: [],
		};
	},

	computed: {
		currentView() {
			if (!this.connected) return "connecting";
			if (!this.nickname) return "nickname";

			if (this.game && (this.game.phase === "playing" || this.game.phase === "finished")) {
				return "game";
			}

			if (this.room && this.room.phase === "lobby") {
				return "room";
			}

			return "home";
		},

		isCreator() {
			return !!(this.room && this.room.creatorId === this.playerId);
		},

		canStart() {
			if (!this.room || !this.room.players) return false;
			const count = this.room.players.length;
			return count >= 2 && count * this.room.initialCards + 1 <= 119;
		},

		isMyTurn() {
			return !!(
				this.game &&
				this.game.phase === "playing" &&
				this.game.turnPlayerId === this.playerId
			);
		},

		canDraw() {
			return !!(
				this.isMyTurn &&
				this.game.stackMode === "none" &&
				!this.game.drawnThisTurn
			);
		},

		canPass() {
			return !!(
				this.isMyTurn &&
				this.game.stackMode === "none" &&
				this.game.drawnThisTurn
			);
		},

		canClaim() {
			return !!(
				this.isMyTurn &&
				(this.game.stackMode === "plus2" || this.game.stackMode === "plus4")
			);
		},

		stackLabel() {
			if (!this.game) return "";
			if (this.game.stackMode === "plus2") {
				return `+2 × ${this.game.stackAmount || 0}`;
			}
			if (this.game.stackMode === "plus4") {
				return `+4 × ${this.game.stackAmount || 0}`;
			}
			return "";
		},

		seats() {
			if (!this.game || !this.game.players || this.game.players.length === 0) {
				return [];
			}

			const arr = this.game.players;
			const n = arr.length;

			let myIdx = arr.findIndex((p) => p.id === this.playerId);
			if (myIdx === -1) myIdx = 0;

			const directionFactor = this.game.direction === -1 ? -1 : 1;
			const result = [];

			for (let i = 0; i < n; i++) {
				const p = arr[(myIdx + i) % n];
				const angle = Math.PI / 2 + directionFactor * (i / n) * 2 * Math.PI;

				result.push({
					...p,
					x: 50 + 42 * Math.cos(angle),
					y: 50 + 42 * Math.sin(angle),
					isTurn: this.game.turnPlayerId === p.id,
				});
			}

			return result;
		},

		targetPlayers() {
			if (!this.modal || this.modal.type !== "target" || !this.game) return [];

			return this.game.players.filter((p) => {
				if (this.modal.includeSelf) return true;
				return p.id !== this.playerId;
			});
		},

		winnerName() {
			if (this.gameOver && this.gameOver.winnerNickname) {
				return this.gameOver.winnerNickname;
			}

			if (this.game && this.game.winnerId && this.game.players) {
				const p = this.game.players.find((x) => x.id === this.game.winnerId);
				if (p) return p.nickname;
			}

			return "";
		},

		pingClass() {
			if (this.ping === null) return "text-gray-400";
			if (this.ping < 150) return "text-green-400";
			if (this.ping < 300) return "text-yellow-400";
			return "text-red-400";
		},
	},

	methods: {
		t(key) {
			const dict = I18N[this.locale] || {};
			return dict[key] !== undefined ? dict[key] : key;
		},

		applyLocale() {
			document.documentElement.lang = this.locale;
			document.documentElement.dir = this.locale === "fa" ? "rtl" : "ltr";
		},

		toggleLocale() {
			this.locale = this.locale === "fa" ? "en" : "fa";
			localStorage.setItem("uno_locale", this.locale);
			this.applyLocale();
		},

		connect() {
			const token = getToken();
			const proto = location.protocol === "https:" ? "wss" : "ws";
			const url = `${proto}://${location.host}/ws?token=${encodeURIComponent(token)}`;

			this.ws = new WebSocket(url);

			this.ws.onopen = () => {
				this.connected = true;
			};

			this.ws.onmessage = (event) => {
				try {
					const msg = JSON.parse(event.data);
					this.onMessage(msg);
				} catch (e) {
					console.error(e);
				}
			};

			this.ws.onclose = () => {
				this.connected = false;
				this.ping = null;
				setTimeout(() => this.connect(), 1000);
			};

			this.ws.onerror = () => {
				// Let onclose handle reconnect.
			};
		},

		send(obj) {
			if (this.ws && this.ws.readyState === WebSocket.OPEN) {
				this.ws.send(JSON.stringify(obj));
			}
		},

		onMessage(msg) {
			switch (msg.type) {
				case "welcome":
					this.playerId = msg.playerId || "";

					// Reset UI state before snapshot.
					this.room = null;
					this.game = null;
					this.hand = [];
					this.gameOver = null;

					if (msg.nickname) {
						this.nickname = msg.nickname;
						this.nicknameInput = msg.nickname;
						this.send({ type: "get_challenges" });
					} else {
						this.nickname = "";
						const saved = localStorage.getItem("uno_nickname");
						if (saved) this.nicknameInput = saved;
					}
					break;

				case "nickname_set":
					this.nickname = msg.nickname;
					localStorage.setItem("uno_nickname", msg.nickname);
					this.send({ type: "get_challenges" });
					break;

				case "challenges":
					this.challenges = msg.challenges || [];
					break;

				case "room_update":
					this.room = msg.room;
					if (this.room && this.room.phase === "lobby") {
						this.game = null;
						this.gameOver = null;
					}
					break;

				case "room_left":
					this.room = null;
					this.game = null;
					this.hand = [];
					this.gameOver = null;
					this.send({ type: "get_challenges" });
					break;

				case "game_started":
					this.gameOver = null;
					break;

				case "game_state":
					this.game = msg;
					if (msg.phase === "playing" || msg.phase === "finished") {
						this.room = null;
					}
					break;

				case "hand":
					this.hand = msg.cards || [];
					break;

				case "game_over":
					this.gameOver = msg;
					break;

				case "pong":
					if (msg.ts !== undefined) {
						this.ping = Date.now() - Number(msg.ts);
					}
					break;

				case "error":
					this.notifyError(msg);
					break;
			}
		},

		notify(text) {
			const id = Date.now() + Math.random();
			this.toasts.push({ id, text });
			setTimeout(() => {
				this.toasts = this.toasts.filter((t) => t.id !== id);
			}, 4000);
		},

		notifyError(msg) {
			const dict = I18N[this.locale] || {};
			const text = dict[msg.code] || msg.message || "Error";
			this.notify(text);
		},

		saveNickname() {
			const nick = this.nicknameInput.trim();
			if (!nick) return;
			this.send({ type: "set_nickname", nickname: nick });
		},

		refreshChallenges() {
			this.send({ type: "get_challenges" });
		},

		createChallenge() {
			this.send({
				type: "create_challenge",
				password: this.createPassword,
				initialCards: Number(this.createCards),
			});
			this.createPassword = "";
		},

		joinChallenge(ch) {
			if (ch.hasPassword) {
				this.selectedChallenge = ch;
				this.passwordInput = "";
				this.modal = { type: "password" };
				return;
			}

			this.send({
				type: "join_challenge",
				roomId: ch.id,
			});
		},

		submitPassword() {
			if (!this.selectedChallenge) return;

			this.send({
				type: "join_challenge",
				roomId: this.selectedChallenge.id,
				password: this.passwordInput,
			});

			this.modal = null;
			this.selectedChallenge = null;
			this.passwordInput = "";
		},

		leaveRoom() {
			this.send({ type: "leave_room" });
			this.room = null;
			this.game = null;
			this.hand = [];
			this.gameOver = null;
		},

		kick(playerId) {
			this.send({ type: "kick_player", playerId });
		},

		updateN(value) {
			this.send({
				type: "update_challenge_settings",
				initialCards: Number(value),
			});
		},

		startGame() {
			this.send({ type: "start_game" });
		},

		cardImageUrl(card) {
			if (!card) return "";
			if (card.color === "wild") {
				return `/cards/${card.value}`;
			}
			return `/cards/${card.color}_${card.value}`;
		},

		canPlay(card) {
			if (!this.game || this.game.phase !== "playing" || !this.isMyTurn) {
				return false;
			}

			if (this.game.stackMode === "plus2") {
				return (
					(card.color !== "wild" && card.value === "draw2") ||
					card.value === "wild_draw4"
				);
			}

			if (this.game.stackMode === "plus4") {
				return card.value === "wild_draw4";
			}

			if (card.color === "wild") return true;
			if (card.color === this.game.activeColor) return true;

			if (
				this.game.topCard &&
				this.game.topCard.color !== "wild" &&
				card.value === this.game.topCard.value
			) {
				return true;
			}

			return false;
		},

		clickCard(card) {
			if (!this.isMyTurn || !this.canPlay(card)) return;

			if (card.value === "wild_color") {
				this.modal = { type: "color", action: "play", cardId: card.id };
				return;
			}

			if (card.value === "shoot") {
				this.modal = {
					type: "target",
					action: "shoot",
					cardId: card.id,
					includeSelf: true,
				};
				return;
			}

			if (card.value === "wild_exchange") {
				this.modal = {
					type: "target",
					action: "exchange",
					cardId: card.id,
					includeSelf: false,
				};
				return;
			}

			this.playCard(card.id);
		},

		playCard(cardId, targetPlayerId = null, chosenColor = null) {
			const payload = { type: "play_card", cardId };

			if (targetPlayerId) payload.targetPlayerId = targetPlayerId;
			if (chosenColor) payload.chosenColor = chosenColor;

			this.send(payload);
		},

		chooseColor(color) {
			if (!this.modal) return;

			if (this.modal.action === "play") {
				this.playCard(this.modal.cardId, null, color);
			} else if (this.modal.action === "claim") {
				this.send({ type: "claim_stack", chosenColor: color });
			}

			this.modal = null;
		},

		chooseTarget(playerId) {
			if (!this.modal) return;
			this.playCard(this.modal.cardId, playerId);
			this.modal = null;
		},

		claimStack() {
			if (!this.game) return;

			if (this.game.stackMode === "plus4") {
				this.modal = { type: "color", action: "claim" };
				return;
			}

			this.send({ type: "claim_stack" });
		},

		drawCard() {
			this.send({ type: "draw" });
		},

		passTurn() {
			this.send({ type: "pass_turn" });
		},

		colorName(color) {
			if (!color) return "-";
			const key = "color_" + color;
			const translated = this.t(key);
			return translated === key ? color : translated;
		},

		colorStyle(color) {
			const map = {
				red: "#dc2626",
				yellow: "#eab308",
				green: "#16a34a",
				blue: "#2563eb",
			};
			return { backgroundColor: map[color] || "#374151" };
		},
	},

	mounted() {
		this.applyLocale();
		this.connect();

		this.pingTimer = setInterval(() => {
			if (this.ws && this.ws.readyState === WebSocket.OPEN) {
				this.send({ type: "ping", ts: Date.now() });
			} else {
				this.ping = null;
			}
		}, 2000);
	},

	beforeUnmount() {
		clearInterval(this.pingTimer);
		if (this.ws) this.ws.close();
	},
}).mount("#app");