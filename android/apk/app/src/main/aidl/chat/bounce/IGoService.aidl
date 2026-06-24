package chat.bounce;

interface IGoService {
    String getInitialState();
    String getEvents();
    void eval(String arg);
}

