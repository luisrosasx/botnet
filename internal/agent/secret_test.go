package agent

import (
	"path/filepath"
	"testing"
	"time"
)

// T-0303 AC1: secret.bin de exactamente 32 B con permisos 0600.
func TestSecretStored32BytesPerm(t *testing.T) {
	dir := t.TempDir()
	stateDir, err := EnsureStateDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	secret := pad32(0xde, 0xad, 0xbe, 0xef)
	if err := SaveSecret(stateDir, secret); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	got, err := LoadSecret(stateDir)
	if err != nil {
		t.Fatalf("LoadSecret: %v", err)
	}
	if string(got) != string(secret) {
		t.Fatal("roundtrip de secreto no conserva los bytes")
	}
	if err := SaveSecret(stateDir, []byte("corto")); err == nil {
		t.Fatal("secreto de 5 B aceptado, want rechazo")
	}
	if !isWindows() {
		ok, err := PrivatePermOK(filepath.Join(stateDir, SecretFileName))
		if err != nil || !ok {
			t.Fatalf("permiso != 0600 (ok=%v err=%v)", ok, err)
		}
	}
}

// T-0303 AC2: doble ventana de rotacion — el secreto viejo firma hasta
// el expires_at y el nuevo firma tras la rotacion.
func TestNodeSecretRotation(t *testing.T) {
	dir := t.TempDir()
	stateDir, err := EnsureStateDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := pad32(1)
	fresh := pad32(2)
	now := time.Now().UTC()
	expires := now.Add(30 * time.Minute) // ventana restante del viejo

	current := StoredSecret{Secret: fresh, IssuedAt: now, ExpiresAt: now.Add(90 * 24 * time.Hour)}
	previous := &StoredSecret{Secret: old, IssuedAt: now.Add(-90 * 24 * time.Hour), ExpiresAt: expires}
	if err := SaveRotation(stateDir, current, previous); err != nil {
		t.Fatalf("SaveRotation: %v", err)
	}
	// secret.bin sincroniza con el current (AC1 sigue cierto).
	if b, err := LoadSecret(stateDir); err != nil || string(b) != string(fresh) {
		t.Fatalf("secret.bin no es el current (err %v)", err)
	}
	loadedCur, loadedPrev, err := LoadRotation(stateDir)
	if err != nil || loadedPrev == nil {
		t.Fatalf("LoadRotation: err %v prev nil = %v", err, loadedPrev == nil)
	}
	if string(loadedCur.Secret) != string(fresh) || string(loadedPrev.Secret) != string(old) {
		t.Fatal("la ventana persistida no calza")
	}

	fields := SessionFields{ClientID: "c1", TenantID: "t1", SessionID: "s1", NodeID: "n1"}
	tokenOld := SignSession(old, fields)
	tokenNew := SignSession(fresh, fields)

	dentro := expires.Add(-5 * time.Minute)
	if !VerifySessionRotated(loadedCur, loadedPrev, tokenOld, dentro) {
		t.Fatal("el secreto viejo debe firmar hasta el expires_at")
	}
	if !VerifySessionRotated(loadedCur, loadedPrev, tokenNew, dentro) {
		t.Fatal("el secreto nuevo debe firmar tras la rotacion")
	}
	despues := expires.Add(time.Minute)
	if VerifySessionRotated(loadedCur, loadedPrev, tokenOld, despues) {
		t.Fatal("el secreto viejo debe estar revocado tras el expires_at")
	}
	if !VerifySessionRotated(loadedCur, loadedPrev, tokenNew, despues) {
		t.Fatal("el secreto nuevo sigue vigente tras expirar el viejo")
	}
}

// T-0303 AC3: la HMAC bot-session es por nodo — verify(token_A,
// secret_B) = false.
func TestSessionHMACPerNode(t *testing.T) {
	secretA := pad32(0xaa)
	secretB := pad32(0xbb)
	fields := SessionFields{ClientID: "c1", TenantID: "t1", SessionID: "s1", NodeID: "nodo-A"}
	tokenA := SignSession(secretA, fields)
	if !VerifySession(secretA, tokenA) {
		t.Fatal("verify(A_token, secret_A) = false, want true")
	}
	if VerifySession(secretB, tokenA) {
		t.Fatal("verify(A_token, secret_B) = true, want false: un router volcado no forja sesiones ajenas")
	}
	if VerifySession(secretA, "basura") {
		t.Fatal("token malformado aceptado")
	}
	cross := SignSession(secretB, SessionFields{ClientID: "c1", TenantID: "t1", SessionID: "s1", NodeID: "nodo-B"})
	if VerifySession(secretA, cross) {
		t.Fatal("token del nodo B valido para el nodo A")
	}
}
