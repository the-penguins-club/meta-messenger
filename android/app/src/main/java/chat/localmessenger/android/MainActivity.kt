package chat.localmessenger.android

import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.Editable
import android.text.TextWatcher
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.widget.*
import org.json.JSONArray
import org.json.JSONObject
import java.text.DateFormat
import java.util.Date

/** Native, recycled Android rows. Message bodies are plain text, never HTML/link launchers. */
class MainActivity : Activity() {
    private val app get() = application as MessengerApp
    private val main = Handler(Looper.getMainLooper())
    private val canvasColor = Color.rgb(12,18,26)
    private val surface = Color.rgb(21,31,43)
    private val accent = Color.rgb(104,224,194)
    private val muted = Color.rgb(154,171,186)
    private lateinit var root: LinearLayout
    private lateinit var status: TextView
    private var active: JSONObject? = null
    private var accountId = ""
    private var query = ""
    private var threads = listOf<JSONObject>()
    private var entries = listOf<JSONObject>()
    private var list: ListView? = null
    private var draft: EditText? = null
    private var busy = false
    private var started = false
    private var refreshQueued = false
    private val serverPoll = object : Runnable {
        override fun run() { if (started) { if (app.serverMode && app.loggedIn) refresh(); main.postDelayed(this,5000) } }
    }

    override fun onCreate(state: Bundle?) {
        super.onCreate(state)
        root = LinearLayout(this).apply { orientation=LinearLayout.VERTICAL; setBackgroundColor(canvasColor) }
        root.setOnApplyWindowInsetsListener { v,insets ->
            if (android.os.Build.VERSION.SDK_INT >= 30) {
                val bars=insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.ime())
                v.setPadding(bars.left,bars.top,bars.right,bars.bottom)
            } else v.setPadding(insets.systemWindowInsetLeft,insets.systemWindowInsetTop,insets.systemWindowInsetRight,insets.systemWindowInsetBottom)
            insets
        }
        setContentView(root)
        showWelcome()
        onBackInvokedDispatcherCompat()
    }

    override fun onStart() {
        super.onStart(); started=true
        app.onEvent={event ->
            when(event.optString("event")) {
                "authenticationRequired" -> { app.needsLogin=true; status.text="Verification required. Return to conversations to sign in again." }
                "engineError" -> status.text=event.optJSONObject("data")?.optString("message")
                "message", "reaction", "conversationsChanged" -> scheduleRefresh()
            }
        }
        loadSession(); main.post(serverPoll)
    }
    override fun onStop() { started=false; main.removeCallbacks(serverPoll);app.onEvent=null;super.onStop() }

    private fun label(value: String, size: Float=16f, color: Int=Color.WHITE) = TextView(this).apply { text=value;textSize=size;setTextColor(color);setPadding(dp(20),dp(10),dp(20),dp(10)) }
    private fun button(value: String, action: ()->Unit) = Button(this).apply { text=value;isAllCaps=false;setTextColor(accent);setOnClickListener{action()} }
    private fun dp(value:Int)=(value*resources.displayMetrics.density).toInt()
    private fun round(color:Int)=GradientDrawable().apply{setColor(color);cornerRadius=dp(18).toFloat()}
    private fun header(title:String,subtitle:String,back:Boolean=false) {
        root.removeAllViews(); list=null;draft=null
        val bar=LinearLayout(this).apply{gravity=Gravity.CENTER_VERTICAL;setPadding(dp(8),dp(8),dp(8),0)}
        if(back)bar.addView(button("‹"){active=null;showThreads();refresh()})
        bar.addView(label(title,25f).apply{setTypeface(null,Typeface.BOLD)},LinearLayout.LayoutParams(0,-2,1f))
        if(!back)bar.addView(button("⋮"){showConnection()})
        root.addView(bar)
        status=label(subtitle,13f,muted);root.addView(status)
    }
    private fun showWelcome() {
        header("Local Messenger","A quieter place for your conversations.")
        root.addView(label("Just your messages.",30f).apply{setPadding(dp(24),dp(72),dp(24),dp(20))})
        root.addView(label("Talk to your people, without the feed, reels, or a built-in browser.",18f,muted))
        root.addView(button("Sign in to Facebook"){login()})
        root.addView(button("Use my own server"){showConnection()})
    }
    private fun loadSession() {
        app.call("status"){value,error ->
            if(!started)return@call
            if(error!=null){status.text=error.message;return@call}
            val data=value as JSONObject
            accountId=data.optJSONObject("account")?.optString("id").orEmpty()
            if(!data.optBoolean("sessionImported")){showWelcome();return@call}
            if(active==null)showThreads() else showChat()
            refresh() // Show durable history immediately, even while offline/reconnecting.
            if(data.optBoolean("connected") && data.optBoolean("encryptedConnected"))return@call
            status.text="Connecting securely…"
            app.call("connect"){connected,problem ->
                if(!started)return@call
                if(problem!=null) { status.text=problem.message; if(app.needsLogin && active==null)root.addView(button("Complete sign-in"){login()}) }
                else {accountId=(connected as JSONObject).optJSONObject("account")?.optString("id").orEmpty();status.text="Connected · encrypted messaging"}
                refresh()
            }
        }
    }
    private fun showThreads() {
        header("Conversations",if(app.serverMode)"Connected through your server" else "On this device")
        val search=EditText(this).apply { hint="Search conversations";setTextColor(Color.WHITE);setHintTextColor(muted);setSingleLine();setPadding(dp(20),dp(8),dp(20),dp(8));setText(query) }
        root.addView(search)
        search.addTextChangedListener(object:TextWatcher{override fun beforeTextChanged(s:CharSequence?,start:Int,count:Int,after:Int){};override fun onTextChanged(s:CharSequence?,start:Int,before:Int,count:Int){query=s.toString();threadRows()};override fun afterTextChanged(e:Editable?){} })
        list=ListView(this).apply{divider=null;setBackgroundColor(canvasColor);setOnItemClickListener{_,_,position,_->active=filtered()[position];showChat();refresh()}}
        root.addView(list,LinearLayout.LayoutParams(-1,0,1f));threadRows()
    }
    private fun filtered()=threads.filter{it.optString("name").contains(query,true)||it.optString("id").contains(query)}
    private fun threadRows(){
        if(active!=null)return
        val items=filtered()
        list?.adapter=object:BaseAdapter(){
            override fun getCount()=items.size;override fun getItem(position:Int)=items[position];override fun getItemId(position:Int)=position.toLong()
            override fun getView(position:Int,convert:View?,parent:ViewGroup):View{
                val row=(convert as? LinearLayout)?:LinearLayout(this@MainActivity).apply{orientation=LinearLayout.VERTICAL;setPadding(dp(4),dp(7),dp(4),dp(7));addView(label("",17f).apply{setTypeface(null,Typeface.BOLD)});addView(label("",13f,muted).apply{maxLines=1;ellipsize=android.text.TextUtils.TruncateAt.END})}
                val item=items[position]
                (row.getChildAt(0)as TextView).text=(item.optString("name").ifBlank{item.getString("id")})+if(item.optBoolean("encrypted"))"  · encrypted" else ""
                (row.getChildAt(1)as TextView).text=item.optString("snippet")
                return row
            }
        }
    }
    private fun showChat(){
        val thread=active?:return
        header(thread.optString("name").ifBlank{thread.getString("id")},if(thread.optBoolean("encrypted"))"End-to-end encrypted · local history" else "Conversation",true)
        list=ListView(this).apply{divider=null;transcriptMode=ListView.TRANSCRIPT_MODE_NORMAL;setOnItemLongClickListener{_,_,position,_->reactions(entries[position]);true}}
        root.addView(list,LinearLayout.LayoutParams(-1,0,1f))
        val composer=LinearLayout(this).apply{gravity=Gravity.CENTER_VERTICAL;setPadding(dp(12),dp(4),dp(8),dp(8))}
        draft=EditText(this).apply{hint="Message";setTextColor(Color.WHITE);setHintTextColor(muted);maxLines=5;background=round(surface);setPadding(dp(16),dp(12),dp(16),dp(12));inputType=android.text.InputType.TYPE_CLASS_TEXT or android.text.InputType.TYPE_TEXT_FLAG_MULTI_LINE or android.text.InputType.TYPE_TEXT_FLAG_CAP_SENTENCES}
        composer.addView(draft,LinearLayout.LayoutParams(0,-2,1f));composer.addView(button("Send"){send()});root.addView(composer)
        entries=emptyList();messageRows()
    }
    private fun messageRows(){
        if(active==null)return
        val oldPosition=list?.lastVisiblePosition?:-1
        val follow=oldPosition>=((list?.adapter?.count?:0)-2)
        list?.adapter=object:BaseAdapter(){
            override fun getCount()=entries.size;override fun getItem(position:Int)=entries[position];override fun getItemId(position:Int)=position.toLong()
            override fun getView(position:Int,convert:View?,parent:ViewGroup):View{
                val row=(convert as? LinearLayout)?:LinearLayout(this@MainActivity).apply{orientation=LinearLayout.VERTICAL;setPadding(dp(16),dp(6),dp(16),dp(6));addView(label("",12f,muted));addView(label("",17f).apply{setTextIsSelectable(false);autoLinkMask=0;linksClickable=false;movementMethod=null;background=round(surface)})}
                val msg=entries[position];val mine=msg.optString("senderId")==accountId
                row.gravity=if(mine)Gravity.END else Gravity.START
                (row.getChildAt(0)as TextView).text=(if(mine)"You" else msg.optString("senderId"))+" · "+DateFormat.getTimeInstance(DateFormat.SHORT).format(Date(msg.optLong("timestamp")))
                (row.getChildAt(1)as TextView).text=msg.optString("text")+(msg.optJSONObject("reactions")?.let{r->r.keys().asSequence().map{r.getString(it)}.joinToString(" ",prefix="\n")}.orEmpty())
                row.getChildAt(1).setOnLongClickListener { reactions(msg); true }
                return row
            }
        }
        if(follow && entries.isNotEmpty())list?.setSelection(entries.lastIndex)
        if(entries.isEmpty())status.text="No cached messages yet. New messages appear here; older encrypted history isn't restored yet."
        else if(status.text.startsWith("No cached messages"))status.text=if(active?.optBoolean("encrypted")==true)"End-to-end encrypted" else "Conversation"
    }
    private fun scheduleRefresh(){if(!refreshQueued){refreshQueued=true;main.postDelayed({refreshQueued=false;if(started)refresh()},150)}}
    private fun refresh(){
        val thread=active
        val params=if(thread==null)JSONObject()else JSONObject().put("conversationId",thread.getString("id")).put("limit",200)
        app.call(if(thread==null)"conversations.list" else "messages.list",params){data,error->
            if(!started || active?.optString("id")!=thread?.optString("id"))return@call
            if(error!=null){status.text=error.message;return@call}
            val array=data as JSONArray;val items=(0 until array.length()).map{array.getJSONObject(it)}
            if(thread==null){threads=items;threadRows()}else{entries=items;messageRows()}
        }
    }
    private fun send(){
        val thread=active?:return;val text=draft?.text?.toString().orEmpty();if(text.isBlank()||busy)return
        busy=true;status.text="Sending…"
        app.call("messages.send",JSONObject().put("conversationId",thread.getString("id")).put("text",text)){_,error->
            busy=false;if(!started)return@call
            if(error==null){if(active?.optString("id")==thread.optString("id"))draft?.text?.clear();status.text="Message accepted";refresh()}else status.text=error.message
        }
    }
    private fun reactions(message:JSONObject){
        val thread=active?:return
        val choices=arrayOf("👍","❤️","😂","😮","😢","Remove my reaction","Copy text")
        AlertDialog.Builder(this).setTitle("React to message").setItems(choices){_,which->
            if (which == 6) {
                (getSystemService(CLIPBOARD_SERVICE) as android.content.ClipboardManager).setPrimaryClip(android.content.ClipData.newPlainText("Message",message.optString("text")))
            } else app.call("messages.react",JSONObject().put("conversationId",thread.getString("id")).put("messageId",message.getString("id")).put("emoji",if(which==5)"" else choices[which])){_,error->if(started){status.text=error?.message?:"Reaction submitted";refresh()}}
        }.show()
    }
    private fun login(){
        app.call("status"){data,error ->
            if(!started)return@call
            if(error!=null){status.text=error.message;return@call}
            if((data as JSONObject).optBoolean("sessionImported") && !app.needsLogin)return@call
            startActivityForResult(Intent(this,LoginActivity::class.java).putExtra("authOnly",true),1)
        }
    }
    @Deprecated("Platform result API keeps this app free of AndroidX")
    override fun onActivityResult(requestCode:Int,resultCode:Int,data:Intent?){
        super.onActivityResult(requestCode,resultCode,data)
        if(requestCode==1 && resultCode==RESULT_OK){
            val cookies=data?.getStringExtra("cookies")?:return
            app.call("session.import",JSONObject().put("cookies",JSONObject(cookies))){_,error->if(error!=null)status.text=error.message else loadSession()}
        }
    }
    private fun showConnection(){
        val layout=LinearLayout(this).apply{orientation=LinearLayout.VERTICAL;setPadding(dp(20),dp(8),dp(20),dp(8))}
        layout.addView(label("Default: connect on this phone. An optional server stays connected when the app is closed and stores your session and decrypted messages.",14f,muted))
        val url=EditText(this).apply{hint="https://messenger.example.com";setText(app.serverAddress);inputType=android.text.InputType.TYPE_CLASS_TEXT or android.text.InputType.TYPE_TEXT_VARIATION_URI}
        val token=EditText(this).apply{hint="Server token";inputType=android.text.InputType.TYPE_CLASS_TEXT or android.text.InputType.TYPE_TEXT_VARIATION_PASSWORD}
        layout.addView(url);layout.addView(token)
        AlertDialog.Builder(this).setTitle("Connection").setView(layout).setPositiveButton("Use server"){_,_->app.configure(url.text.toString(),token.text.toString()){error->if(error!=null)status.text=error.message else {active=null;loadSession()}}}
            .setNeutralButton("Use this phone"){_,_->app.configure("",""){error->if(error!=null)status.text=error.message else {active=null;loadSession()}}}.setNegativeButton("Cancel",null).show()
    }
    private fun back(){if(active!=null){active=null;showThreads();refresh()}else finish()}
    private fun onBackInvokedDispatcherCompat(){if(android.os.Build.VERSION.SDK_INT>=33)onBackInvokedDispatcher.registerOnBackInvokedCallback(android.window.OnBackInvokedDispatcher.PRIORITY_DEFAULT){back()}}
    // Android 13+ uses the platform OnBackInvokedDispatcher registered above.
    @android.annotation.SuppressLint("GestureBackNavigation")
    @Deprecated("Compatibility for Android 8–12")override fun onBackPressed(){back()}
}
